package ingest_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// fakeConn/fakeTx/fakeConnector simulate just enough of database/sql/driver
// to let database/sql's own connection-pool and transaction bookkeeping run
// for real, without a live Postgres. execDelay controls how long each
// ExecContext call takes, which lets tests force a deadline to land either
// before any connection is acquired or mid-way through CreateJob's
// insert loop.
type fakeConn struct {
	execDelay  time.Duration
	committed  *atomic.Bool
	rolledBack *atomic.Bool
}

func (fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fakeConn: not implemented (ExecerContext is used instead)")
}
func (fakeConn) Close() error { return nil }
func (c fakeConn) Begin() (driver.Tx, error) {
	return fakeTx{committed: c.committed, rolledBack: c.rolledBack}, nil
}

// ExecContext honours ctx cancellation: if the deadline fires before
// execDelay elapses, it returns ctx.Err() instead of completing - the same
// choice a real driver makes when its underlying network write/read is
// cancelled mid-flight.
func (c fakeConn) ExecContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Result, error) {
	select {
	case <-time.After(c.execDelay):
		return driver.RowsAffected(1), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type fakeTx struct {
	committed  *atomic.Bool
	rolledBack *atomic.Bool
}

func (tx fakeTx) Commit() error {
	if tx.committed != nil {
		tx.committed.Store(true)
	}
	return nil
}

func (tx fakeTx) Rollback() error {
	if tx.rolledBack != nil {
		tx.rolledBack.Store(true)
	}
	return nil
}

// fakeConnector avoids the sql.Register/DSN-string indirection: each test
// gets its own connector instance parameterised with its own execDelay and
// counters, so tests cannot collide on a shared global driver name.
type fakeConnector struct {
	execDelay  time.Duration
	committed  *atomic.Bool
	rolledBack *atomic.Bool
}

func (c fakeConnector) Connect(context.Context) (driver.Conn, error) {
	return fakeConn(c), nil
}
func (c fakeConnector) Driver() driver.Driver { return fakeStubDriver{} }

// fakeStubDriver only exists to satisfy driver.Connector.Driver(); Open is
// never called because all connections in these tests go through Connect.
type fakeStubDriver struct{}

func (fakeStubDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fakeStubDriver: Open should be unreachable, tests use driver.Connector")
}

// openSingleConnDB returns a *sql.DB capped at one connection, mirroring the
// single shared *sql.DB (SetMaxOpenConns(10) in cmd/tatara-memory/app.go)
// that tatara-memory#85/#86 traced: one stuck backend exhausts every slot in
// the pool for every other caller of the same *sql.DB.
func openSingleConnDB(t *testing.T, connector driver.Connector) *sql.DB {
	t.Helper()
	db := sql.OpenDB(connector)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	return db
}

// holdOnlyConnection occupies the pool's single connection slot in an open,
// never-committed transaction - the exact shape of the leaked transaction
// tatara-memory#86 found on mem-mtg-pg (opened 06:03:47Z, never committed or
// rolled back). Returns once the connection is confirmed acquired. Assertion
// failures inside the goroutine use assert (not require): require calls
// t.FailNow, which testing documents must run on the test's own goroutine.
// The holder is released via t.Context(), which testing cancels when the
// test completes, so the goroutine cannot leak past the test.
func holdOnlyConnection(t *testing.T, db *sql.DB) {
	t.Helper()
	acquired := make(chan struct{})
	go func() {
		tx, err := db.BeginTx(context.Background(), nil)
		if !assert.NoError(t, err) {
			close(acquired)
			return
		}
		close(acquired)
		<-t.Context().Done() // released when the test completes
		_ = tx.Rollback()
	}()
	<-acquired
}

// TestPGStore_CreateJob_AcquireTimeout is the regression test for
// tatara-memory#85/#86: with the pool's only connection held by a stuck
// transaction, CreateJob's own BeginTx must fail fast with
// context.DeadlineExceeded instead of parking until the caller's context is
// cancelled or done. The call is raced against a generous outer bound so the
// test itself cannot hang even if the fix regresses.
func TestPGStore_CreateJob_AcquireTimeout(t *testing.T) {
	db := openSingleConnDB(t, fakeConnector{})
	holdOnlyConnection(t, db)

	store := ingest.NewPGStore(db, ingest.WithCreateJobTimeout(50*time.Millisecond))
	job := memory.IngestJob{
		ID:        "job-acquire-timeout",
		Status:    memory.JobStatusQueued,
		Total:     1,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	items := []memory.IngestItem{{IdempotencyKey: "k1", Text: "x"}}

	type result struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		err := store.CreateJob(context.Background(), job, items)
		done <- result{err: err, elapsed: time.Since(start)}
	}()

	select {
	case r := <-done:
		require.Error(t, r.err, "CreateJob must fail, not silently succeed, when the pool is exhausted")
		require.True(t, errors.Is(r.err, context.DeadlineExceeded),
			"want context.DeadlineExceeded (fast-fail path -> 503+Retry-After), got: %v", r.err)
		require.Less(t, r.elapsed, 2*time.Second,
			"CreateJob must bound the pool acquire itself, not rely on the caller's context")
	case <-time.After(5 * time.Second):
		t.Fatal("CreateJob hung past its own createJobTimeout - the acquire is unbounded, reproducing tatara-memory#85/#86's 300s parked request")
	}
}

// TestPGStore_CreateJob_NoTimeoutConfigured_StillBoundedByCallerContext
// documents the opt-out: a zero createJobTimeout (WithCreateJobTimeout not
// called) restores the pre-fix behaviour of relying solely on whatever
// deadline the caller's context carries. This is exercised via a
// caller-supplied deadline rather than an unbounded wait so the test itself
// cannot hang.
func TestPGStore_CreateJob_NoTimeoutConfigured_StillBoundedByCallerContext(t *testing.T) {
	db := openSingleConnDB(t, fakeConnector{})
	holdOnlyConnection(t, db)

	store := ingest.NewPGStore(db) // no WithCreateJobTimeout: opt-out preserved
	job := memory.IngestJob{ID: "job-no-timeout", Status: memory.JobStatusQueued, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	items := []memory.IngestItem{{IdempotencyKey: "k1", Text: "x"}}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := store.CreateJob(ctx, job, items)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded))
}

// TestPGStore_CreateJob_DeadlineFiresMidInsertLoop is the direct test for
// decision 2 in the #85/#86 fix: WithCreateJobTimeout bounds CreateJob's
// *whole* transaction, not just the initial BeginTx acquire. BeginTx here
// succeeds immediately (no contention); execDelay is tuned so the deadline
// fires while CreateJob is still inside its per-item ExecContext loop,
// after the job row and the first item have already been inserted. The
// point under test: this must still produce a clean context.DeadlineExceeded
// (mapped by errmap.go to 503+Retry-After) with the transaction rolled back
// - not a partial commit, and not some other error class (e.g. sql.ErrTxDone
// racing the context-cancellation-driven auto-rollback that database/sql
// itself runs against the same BeginTx context) leaking through as a 500.
func TestPGStore_CreateJob_DeadlineFiresMidInsertLoop(t *testing.T) {
	var committed, rolledBack atomic.Bool
	connector := fakeConnector{
		execDelay:  100 * time.Millisecond,
		committed:  &committed,
		rolledBack: &rolledBack,
	}
	db := openSingleConnDB(t, connector)

	// createJobTimeout falls between one and two ExecContext calls: the job
	// row insert (~100ms) succeeds, the first item insert starts around
	// 100ms with ~150ms left before the 250ms deadline (comfortable
	// scheduling margin), so the deadline fires during that call rather than
	// at BeginTx or at Commit.
	store := ingest.NewPGStore(db, ingest.WithCreateJobTimeout(250*time.Millisecond))
	job := memory.IngestJob{ID: "job-mid-loop", Status: memory.JobStatusQueued, Total: 2, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	items := []memory.IngestItem{
		{IdempotencyKey: "k1", Text: "x"},
		{IdempotencyKey: "k2", Text: "y"},
	}

	type result struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		err := store.CreateJob(context.Background(), job, items)
		done <- result{err: err, elapsed: time.Since(start)}
	}()

	select {
	case r := <-done:
		require.Error(t, r.err, "a deadline landing mid-insert-loop must fail the call")
		require.True(t, errors.Is(r.err, context.DeadlineExceeded),
			"want context.DeadlineExceeded (-> 503+Retry-After in errmap.go), got a different error class: %v", r.err)
		require.Less(t, r.elapsed, 2*time.Second, "must fail near the configured deadline, not hang")
		require.False(t, committed.Load(), "must never commit a transaction whose insert loop was cut short - that would be a partial write, worse than a clean failure")
		require.True(t, rolledBack.Load(), "the transaction must be rolled back, either by CreateJob's own deferred Rollback or database/sql's own context-cancellation auto-rollback")
	case <-time.After(5 * time.Second):
		t.Fatal("CreateJob hung past its own createJobTimeout while mid-insert-loop")
	}
}
