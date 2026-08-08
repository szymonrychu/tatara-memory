package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// errStartupPending is what /readyz reports before the first probe has even
// finished, so "not ready yet" is never an empty error.
var errStartupPending = errors.New("database startup has not completed yet")

// startupGate is the readiness half of tatara-memory#102's fix.
//
// The database-dependent part of startup - waiting for Postgres, applying the
// schema migrations, resuming unfinished ingest jobs - used to run on the
// critical path of process start, with a hardcoded 60s budget whose expiry
// became os.Exit(1). It now runs behind this gate instead: the HTTP server is
// listening the whole time, /healthz is green so the kubelet does not restart a
// pod that is merely waiting on its dependency, and /readyz reports exactly why
// the pod is not taking traffic. A dependency outage is a readiness condition,
// not a reason to terminate.
type startupGate struct {
	mu       sync.Mutex
	ready    bool
	attempts int
	last     error
}

// record stores the outcome of one startup attempt. A nil err is stored too, so
// the reported reason never outlives the failure it describes.
func (g *startupGate) record(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err != nil {
		g.attempts++
	}
	g.last = err
}

// markReady closes the gate for good.
func (g *startupGate) markReady() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ready = true
	g.last = nil
}

// status returns nil once startup has completed, otherwise why it has not.
func (g *startupGate) status() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ready {
		return nil
	}
	if g.last != nil {
		return fmt.Errorf("incomplete after %d failed attempt(s): %w", g.attempts, g.last)
	}
	return errStartupPending
}

// startupStatus reports nil when the app is serving with a live, migrated
// database, and otherwise the reason it is not. readyzFunc surfaces it.
func (a *app) startupStatus() error {
	if a.startup == nil {
		return nil
	}
	return a.startup.status()
}

// startupReady is startupStatus as a predicate, for probes and tests.
func (a *app) startupReady() bool { return a.startupStatus() == nil }

// awaitDatabase runs the database-dependent half of startup off the serving
// path, retrying with backoff until it succeeds, the budget expires, or the
// process shuts down. It owns closing startupDone.
//
// timeout is DB_WAIT_TIMEOUT and defaults to 0 (unlimited): with a bounded
// budget the process merely stops retrying and stays unready - it still never
// exits, because exiting is what made the incident's outage longer than its
// cause.
func (a *app) awaitDatabase(ctx context.Context, timeout, interval time.Duration) {
	defer close(a.startupDone)

	if err := waitForDB(ctx, a.startupAttempt, timeout, interval); err != nil {
		a.startup.record(err)
		if ctx.Err() != nil {
			a.log.Info("startup wait cancelled", "action", "db_wait_cancelled")
			return
		}
		// Deliberately not fatal: an unready pod keeps the previous ReplicaSet
		// serving and leaves a live process to diagnose, where an exit only
		// produces CrashLoopBackOff (tatara-memory#102).
		a.log.Error("gave up waiting for postgres; staying unready",
			"action", "db_wait_giveup", "timeout", timeout.String(), "error", err)
		return
	}

	a.startup.markReady()
	a.log.Info("database ready", "action", "db_ready")
}

// startupAttempt is one whole attempt at becoming ready: reach Postgres, apply
// the schema, resume unfinished ingest jobs. It is retried as a unit because a
// Postgres that answers a ping and then disappears mid-migration is the same
// outage, and every step is idempotent.
func (a *app) startupAttempt(ctx context.Context) error {
	err := a.startupAttemptOnce(ctx)
	a.startup.record(err)
	if err != nil {
		a.log.Warn("waiting for postgres", "action", "db_wait", "error", err)
	}
	return err
}

func (a *app) startupAttemptOnce(ctx context.Context) error {
	if err := a.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	if err := a.migrate(ctx); err != nil {
		return err
	}
	// Resume is best-effort, exactly as it was when it ran inline in
	// newAppWithDeps: a job that cannot be resumed is not a reason to hold the
	// whole process unready.
	if a.pool != nil {
		if n, err := a.pool.Resume(ctx); err != nil {
			a.log.Error("ingest pool resume failed", "error", err)
		} else if n > 0 {
			a.log.Info("ingest pool resumed unfinished jobs", "jobs", n)
		}
	}
	return nil
}
