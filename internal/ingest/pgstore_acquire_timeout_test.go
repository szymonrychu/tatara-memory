package ingest_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// fakeConn/fakeTx/fakeDriver simulate just enough of database/sql/driver to
// let database/sql's own connection-pool bookkeeping run for real, without a
// live Postgres. No SQL is actually parsed or executed: the test never lets a
// second BeginTx acquire a connection, so no statement is ever prepared.
type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fakeConn: not implemented")
}
func (fakeConn) Close() error              { return nil }
func (fakeConn) Begin() (driver.Tx, error) { return fakeTx{}, nil }

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return fakeConn{}, nil }

func init() {
	sql.Register("tatara-fake-pgstore-acquire", fakeDriver{})
}

// openSingleConnDB returns a *sql.DB capped at one connection, mirroring the
// single shared *sql.DB (SetMaxOpenConns(10) in cmd/tatara-memory/app.go)
// that tatara-memory#85/#86 traced: one stuck backend exhausts every slot in
// the pool for every other caller of the same *sql.DB.
func openSingleConnDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("tatara-fake-pgstore-acquire", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	return db
}

// holdOnlyConnection occupies the pool's single connection slot in an open,
// never-committed transaction - the exact shape of the leaked transaction
// tatara-memory#86 found on mem-mtg-pg (opened 06:03:47Z, never committed or
// rolled back). Returns once the connection is confirmed acquired.
func holdOnlyConnection(t *testing.T, db *sql.DB) {
	t.Helper()
	acquired := make(chan struct{})
	go func() {
		tx, err := db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		close(acquired)
		<-context.Background().Done() // never fires: hold the tx forever, like the incident
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
	db := openSingleConnDB(t)
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
	db := openSingleConnDB(t)
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
