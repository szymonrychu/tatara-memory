// Package pgmigrate is the one version-tracked schema migration runner used by
// every package in this service that owns Postgres tables.
//
// It exists because "migrations are idempotent, so replaying them is free" held
// for exactly one of the three packages that relied on it (tatara-memory#107).
// codegraph/0005 DROPped and re-ADDed code_edges_pkey on every process start:
// an ACCESS EXCLUSIVE index rebuild on the largest table in the graph, issued
// on a pooled connection carrying the request-path lock_timeout, during a
// rolling upgrade in which the outgoing pod is still serving. A tracker turns
// "the statement must be re-runnable" into "the statement is never re-run",
// which is the property the migrations actually needed.
package pgmigrate

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"time"
)

const (
	// StatementTag prefixes every statement the runner issues on its own behalf:
	// the tracker bootstrap, the applied check, the per-transaction timeout SETs,
	// the baseline probes and the tracker insert. Migration SQL is never tagged.
	// Production value: attribution in pg_stat_activity / pg_stat_statements.
	// Test value: a recorder can separate runner bookkeeping from schema DDL
	// exactly, rather than by matching a hand-maintained list of SQL fragments.
	StatementTag = "-- pgmigrate: "

	// AppliedCheckTag identifies the tracker read that asks whether a named
	// migration has already been applied.
	AppliedCheckTag = StatementTag + "applied check"
	// StampTag identifies the tracker insert that records a migration as applied.
	StampTag = StatementTag + "stamp"

	// migrationLockTimeout and migrationStatementTimeout replace, for the
	// duration of one migration transaction, the session GUCs that
	// cmd/tatara-memory layers onto every pooled connection.
	//
	// PG_LOCK_TIMEOUT defaults to 30s because that is a sane bound on a
	// REQUEST-path lock wait (tatara-memory#98): a read or write queued behind
	// an abandoned transaction should give up quickly. A schema migration is not
	// that shape. It legitimately queues behind /code-graph:bulk's single
	// Reconcile transaction, which runs 50-194s+ on a large repo, and a migration
	// that gives up at 30s does not stop taking the lock - it retries through the
	// startup backoff, and each attempt queues a fresh ACCESS EXCLUSIVE request
	// that blocks every later lock request on the table behind it for the whole
	// of its own wait. One longer wait is strictly cheaper than N short ones, so
	// the bound has to clear the documented worst case rather than sit inside it:
	// 5m is 194s plus headroom.
	//
	// statement_timeout defaults to 5m, which is a backstop against a pathological
	// query, not a budget for an index build on the largest table in the graph.
	//
	// Deliberately not env-configurable: the PG_* knobs were incident responses
	// with an operator asking for them, and three MigrateWith variants plus config
	// plumbing is real API surface for a knob nobody has requested.
	migrationLockTimeout      = 5 * time.Minute
	migrationStatementTimeout = 30 * time.Minute
)

// safeTrackerName guards the one identifier this package interpolates into DDL.
var safeTrackerName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// Querier is the read surface a baseline probe needs. Both *sql.DB and *sql.Tx
// satisfy it; the runner always passes the migration's own transaction, so a
// probe and the tracker row it produces are decided on one snapshot.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Migration is one named, ordered unit of schema change.
type Migration struct {
	// Name is recorded in the tracker table. It never changes once shipped:
	// renaming a migration re-runs it against every database that already has it.
	Name string
	// SQL is the migration body. It is executed with no bind parameters, so pgx
	// uses the simple query protocol and multi-statement bodies run as one
	// implicit transaction inside the runner's explicit one.
	//
	// It therefore may NOT contain a statement that refuses to run inside a
	// transaction block - CREATE INDEX CONCURRENTLY, VACUUM, ALTER SYSTEM - which
	// fail with SQLSTATE 25001. The codegraph migrations used to run in
	// autocommit and so could have used CONCURRENTLY; none did. If a future index
	// on code_edges needs it, add an explicit opt-out here rather than shipping a
	// blocking CREATE INDEX, which is the failure class this package exists for.
	SQL string
	// AlreadyApplied is an optional FINAL-STATE probe. When it reports that this
	// migration's end state is already present, the tracker row is stamped and
	// SQL is NOT executed.
	//
	// A probe is consulted whenever no migration has yet had to execute in this
	// run - which, once a database is fully stamped, is every run. It is
	// therefore AUTHORITATIVE for the migration it guards: a probe that can be
	// satisfied for a reason unrelated to its own migration skips that migration
	// permanently, everywhere. Test the migration's exact end state, or leave it
	// nil and let the SQL run.
	AlreadyApplied func(ctx context.Context, q Querier) (bool, error)
}

// Runner applies an ordered set of migrations, recording each in Tracker.
type Runner struct {
	// Tracker is the per-package tracking table name. It is interpolated into
	// DDL, so it is validated rather than parameterised.
	Tracker    string
	Migrations []Migration
}

// ProbeTag returns the statement prefix Probe attaches to name's query.
func ProbeTag(name string) string { return StatementTag + "baseline probe " + name }

// Probe builds an AlreadyApplied func from a query returning exactly one
// non-null boolean. Write the query against pg_catalog so it reads the schema's
// real end state and not a tracker the database does not have yet.
func Probe(name, query string) func(context.Context, Querier) (bool, error) {
	tagged := ProbeTag(name) + "\n" + query
	return func(ctx context.Context, q Querier) (bool, error) {
		var present bool
		if err := q.QueryRowContext(ctx, tagged).Scan(&present); err != nil {
			return false, err
		}
		return present, nil
	}
}

// BootstrapSQL returns the DDL that creates the tracker table.
func (r Runner) BootstrapSQL() string {
	return StatementTag + "bootstrap tracker\n" +
		"CREATE TABLE IF NOT EXISTS " + r.Tracker + " (\n" +
		"    name       TEXT PRIMARY KEY,\n" +
		"    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()\n" +
		")"
}

// Names returns the ordered migration names.
func (r Runner) Names() []string {
	names := make([]string, len(r.Migrations))
	for i, m := range r.Migrations {
		names[i] = m.Name
	}
	return names
}

// SQL returns every migration body concatenated, in order. It describes the
// schema a fresh database ends up with; it is not what Run executes.
func (r Runner) SQL() string {
	var b []byte
	for i, m := range r.Migrations {
		if i > 0 {
			b = append(b, '\n')
		}
		b = append(b, m.SQL...)
	}
	return string(b)
}

// Run applies every unapplied migration, in order, each in its own transaction.
//
// Baselining (tatara-memory#107, option B1) handles the databases that were
// migrated before this package existed and so have no tracker rows: while no
// migration has yet had to execute for real, each one's AlreadyApplied probe is
// consulted, and a migration whose end state is already present is stamped
// without running. The FIRST migration that must actually execute ends
// baselining - every later migration then runs unconditionally, even if its own
// probe would have passed. A database stuck at 0001 therefore cannot be stamped
// as fully migrated by a later probe that happens to be satisfied for an
// unrelated reason: the executed 0002 has already ended baselining.
//
// A migration SKIPPED because its tracker row exists does NOT end baselining,
// and that is deliberate: a pod killed part-way through the first baselining run
// must resume baselining the rest on the next boot, not fall back to executing
// 0005's ACCESS EXCLUSIVE pkey rebuild against a database that already has it.
// The consequence is that on a fully stamped database baselining is still in
// effect, so a NEW migration's probe is authoritative there too. See
// Migration.AlreadyApplied: a probe is a promise about that migration's exact
// end state, not a hint.
func (r Runner) Run(ctx context.Context, db *sql.DB) error {
	if !safeTrackerName.MatchString(r.Tracker) {
		return fmt.Errorf("pgmigrate: unsafe tracker table name %q", r.Tracker)
	}
	if _, err := db.ExecContext(ctx, r.BootstrapSQL()); err != nil {
		return fmt.Errorf("pgmigrate %s: create tracker: %w", r.Tracker, err)
	}

	baselining := true
	for _, m := range r.Migrations {
		applied, err := r.applied(ctx, db, m.Name)
		if err != nil {
			return fmt.Errorf("pgmigrate %s: check %s: %w", r.Tracker, m.Name, err)
		}
		if applied {
			continue
		}
		executed, err := r.apply(ctx, db, m, baselining)
		if err != nil {
			return fmt.Errorf("pgmigrate %s: apply %s: %w", r.Tracker, m.Name, err)
		}
		if executed {
			baselining = false
		}
	}
	return nil
}

func (r Runner) applied(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx,
		AppliedCheckTag+"\nSELECT EXISTS (SELECT 1 FROM "+r.Tracker+" WHERE name = $1)", name).
		Scan(&exists)
	return exists, err
}

// apply runs one migration in its own transaction and reports whether the
// migration SQL was actually executed (false means the row was baselined).
func (r Runner) apply(ctx context.Context, db *sql.DB, m Migration, baselining bool) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, sessionTimeoutsSQL()); err != nil {
		return false, fmt.Errorf("session timeouts: %w", err)
	}

	execute := true
	if baselining && m.AlreadyApplied != nil {
		present, err := m.AlreadyApplied(ctx, tx)
		if err != nil {
			return false, fmt.Errorf("baseline probe: %w", err)
		}
		execute = !present
	}
	if execute {
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			return false, fmt.Errorf("exec: %w", err)
		}
	}

	// ON CONFLICT DO NOTHING makes the tracker insert idempotent so that two
	// replicas racing through the same migration (TOCTOU between the applied
	// check and the apply tx) do not conflict on the PK: both succeed, with one
	// of them being a no-op on the tracker row. That race is only benign while
	// the migration body is itself re-runnable, which is why a migration that is
	// NOT - codegraph/0005 - must still ship a baseline probe rather than rely on
	// the tracker alone.
	if _, err := tx.ExecContext(ctx,
		StampTag+"\nINSERT INTO "+r.Tracker+" (name) VALUES ($1) ON CONFLICT (name) DO NOTHING",
		m.Name); err != nil {
		return false, fmt.Errorf("record: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return execute, nil
}

func sessionTimeoutsSQL() string {
	return fmt.Sprintf("%ssession timeouts\nSET LOCAL lock_timeout = %d;\nSET LOCAL statement_timeout = %d;",
		StatementTag, migrationLockTimeout.Milliseconds(), migrationStatementTimeout.Milliseconds())
}
