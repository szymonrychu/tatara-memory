package memory

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

// TombstoneReapBatchSize is the maximum number of tombstones processed per tick
// via the confirm (TrackStatus 404) path. A 24h forced-TTL backstop handles any
// backlog beyond this limit. 1000 is chosen to keep each tick well under 5 min
// even at the upstream's slowest observed response time (~200ms/id).
const TombstoneReapBatchSize = 1000

// forceReapBackoffBase and forceReapBackoffCap bound the delay between
// successive force-reap re-verifications of a tombstone whose upstream doc is
// still present. The delay doubles per attempt starting at forceReapBackoffBase
// and is capped at forceReapBackoffCap, so a permanently-stuck upstream delete
// settles into a once-a-day self-heal probe instead of being re-checked (and
// re-warned) on every 5-minute tick forever.
const (
	forceReapBackoffBase = time.Hour
	forceReapBackoffCap  = 24 * time.Hour
)

// forceReapBackoff returns the delay before the next force-recheck of a
// tombstone that has been force-checked attempts times (the count AFTER the
// current check). attempts <= 1 returns the base delay.
func forceReapBackoff(attempts int) time.Duration {
	d := forceReapBackoffBase
	for i := 1; i < attempts && d < forceReapBackoffCap; i++ {
		d *= 2
	}
	if d > forceReapBackoffCap {
		d = forceReapBackoffCap
	}
	return d
}

// trackStatuser is the subset of the lightrag client used by the reaper.
// Defining it here keeps the package boundary narrow and the reaper testable
// with a fake.
type trackStatuser interface {
	TrackStatus(ctx context.Context, trackID string) (*lightrag.TrackStatusResponse, error)
}

// reapStore is the minimal store interface the Reaper needs. *TombstoneStore
// satisfies it; tests may inject a fake.
type reapStore interface {
	List(ctx context.Context, limit int) ([]string, error)
	Delete(ctx context.Context, id string) error
	ListOlderThan(ctx context.Context, maxAge time.Duration, limit int) ([]ForceCandidate, error)
	RecordForceCheckStillPresent(ctx context.Context, id string, nextCheckAt time.Time) error
}

// Reaper periodically removes tombstones once lightrag confirms the document
// has been deleted, plus a 24h forced sweep that re-verifies each aged
// tombstone upstream before deleting (skipping still-present docs).
type Reaper struct {
	store    reapStore
	lightrag trackStatuser
	logger   *slog.Logger
	interval time.Duration
	maxAge   time.Duration
	metric   *prometheus.CounterVec
}

// NewReaper constructs a Reaper. Registers tatara_memory_tombstone_total{op}
// against the given registerer.
func NewReaper(store *TombstoneStore, lr trackStatuser, logger *slog.Logger, reg prometheus.Registerer) *Reaper {
	return newReaper(store, lr, logger, reg)
}

func newReaper(store reapStore, lr trackStatuser, logger *slog.Logger, reg prometheus.Registerer) *Reaper {
	m := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tatara_memory_tombstone_total",
			Help: "Tombstone operations by type (reaped, forced, force_skipped_still_present, created, check_error)",
		},
		[]string{"op"},
	)
	reg.MustRegister(m)
	// Pre-initialize all label values so Gather always returns the family.
	for _, op := range []string{"reaped", "forced", "force_skipped_still_present", "created", "check_error"} {
		m.WithLabelValues(op)
	}
	return &Reaper{
		store:    store,
		lightrag: lr,
		logger:   logger,
		interval: 5 * time.Minute,
		maxAge:   24 * time.Hour,
		metric:   m,
	}
}

// IncCreated increments tatara_memory_tombstone_total{op="created"}.
// Called by TombstoneStore.Mark via SetMarkCounter.
func (r *Reaper) IncCreated() {
	r.metric.WithLabelValues("created").Inc()
}

// Run blocks until ctx is canceled, ticking every interval.
func (r *Reaper) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.tick(ctx)
		}
	}
}

func (r *Reaper) tick(ctx context.Context) {
	// Bound the whole tick (fast + force paths) to the interval so a slow upstream
	// cannot stretch a tick past the next scheduled fire.
	tickCtx, cancel := context.WithTimeout(ctx, r.interval)
	defer cancel()

	ids, err := r.store.List(tickCtx, TombstoneReapBatchSize)
	if err != nil {
		r.logger.Error("tombstone list", "err", err)
		return
	}
	for _, id := range ids {
		if err := tickCtx.Err(); err != nil {
			return // per-tick deadline exhausted; next tick resumes
		}
		resp, err := r.lightrag.TrackStatus(tickCtx, id)
		if err != nil {
			var he *lightrag.HTTPError
			if errors.As(err, &he) && he.Status == http.StatusNotFound {
				r.reapConfirmed(tickCtx, id)
			} else {
				r.metric.WithLabelValues("check_error").Inc()
				r.logger.Warn("tombstone check error", "track_id", id, "err", err)
			}
		} else if resp == nil || len(resp.Documents) == 0 {
			// HTTP 200 with empty (or nil) Documents: upstream has no record of this
			// doc, treat as confirmed gone (mirrors GetMemory's own ErrNotFound path).
			r.reapConfirmed(tickCtx, id)
		}
		// else: err==nil and docs present -> still being deleted, leave tombstone
	}
	r.forceTick(tickCtx)
}

// forceTick is the 24h forced-reap path. Unlike the fast path, it calls
// TrackStatus per candidate id and only deletes tombstones whose upstream doc
// is confirmed gone (404 or empty-docs). Tombstones whose doc is still present
// are skipped and counted as force_skipped_still_present, and their next
// re-verification is deferred via an exponential backoff (forceReapBackoff) so
// a permanently-stuck upstream delete is not re-checked (and re-warned) on
// every tick forever - ListOlderThan already excludes candidates whose
// next_force_check_at has not elapsed.
func (r *Reaper) forceTick(ctx context.Context) {
	aged, err := r.store.ListOlderThan(ctx, r.maxAge, TombstoneReapBatchSize)
	if err != nil {
		r.logger.Error("tombstone list older", "err", err)
		return
	}
	for _, cand := range aged {
		if err := ctx.Err(); err != nil {
			return
		}
		id := cand.TrackID
		resp, err := r.lightrag.TrackStatus(ctx, id)
		if err != nil {
			var he *lightrag.HTTPError
			if errors.As(err, &he) && he.Status == http.StatusNotFound {
				// confirmed gone via 404
				r.reapConfirmedForced(ctx, id)
			} else {
				// transient / unknown: keep tombstone, log WARN
				r.metric.WithLabelValues("check_error").Inc()
				r.logger.Warn("tombstone force-check error", "track_id", id, "err", err)
			}
		} else if resp == nil || len(resp.Documents) == 0 {
			// 200 + empty: confirmed gone
			r.reapConfirmedForced(ctx, id)
		} else {
			// doc still present: defer the next check via backoff, emit
			// observable metric + WARN (attempts/next_check_at make the
			// staleness readable without cross-referencing the DB).
			attempts := cand.Attempts + 1
			nextCheck := time.Now().Add(forceReapBackoff(attempts))
			if rerr := r.store.RecordForceCheckStillPresent(ctx, id, nextCheck); rerr != nil {
				r.logger.Error("tombstone record force check", "track_id", id, "err", rerr)
			}
			r.metric.WithLabelValues("force_skipped_still_present").Inc()
			r.logger.Warn("tombstone force-reap skipped: doc still present",
				"track_id", id, "attempts", attempts, "next_check_at", nextCheck)
		}
	}
}

func (r *Reaper) reapConfirmed(ctx context.Context, id string) {
	if derr := r.store.Delete(ctx, id); derr == nil {
		r.metric.WithLabelValues("reaped").Inc()
		r.logger.Info("tombstone reaped", "track_id", id)
	} else {
		r.logger.Error("tombstone reap delete", "track_id", id, "err", derr)
	}
}

func (r *Reaper) reapConfirmedForced(ctx context.Context, id string) {
	if derr := r.store.Delete(ctx, id); derr == nil {
		r.metric.WithLabelValues("forced").Inc()
		r.logger.Info("tombstone forced reap confirmed", "track_id", id)
	} else {
		r.logger.Error("tombstone forced reap delete", "track_id", id, "err", derr)
	}
}
