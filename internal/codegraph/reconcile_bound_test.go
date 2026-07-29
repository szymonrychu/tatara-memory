package codegraph_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

// The fakes below are the smallest database/sql/driver shims that let
// PGStore.Reconcile run its real BeginTx -> Exec* -> Commit sequence without a
// live Postgres, mirroring internal/httpapi's bulk_ingest_deadline_test.go.
// sql.OpenDB(connector) is used rather than sql.Register+sql.Open so each test
// owns an isolated connector with no shared global driver name.
//
// execFn decides what every statement does: block, fail with a specific
// SQLSTATE, or succeed. recorded captures the statement text in order so a test
// can assert on what Reconcile actually sent.
type boundFakeConn struct {
	mu       sync.Mutex
	recorded *[]string
	exec     func(ctx context.Context, query string) (driver.Result, error)
}

func (c *boundFakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("boundFakeConn: Prepare should be unreachable, ExecerContext is implemented")
}
func (c *boundFakeConn) Close() error              { return nil }
func (c *boundFakeConn) Begin() (driver.Tx, error) { return boundFakeTx{}, nil }

func (c *boundFakeConn) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.mu.Lock()
	*c.recorded = append(*c.recorded, query)
	c.mu.Unlock()
	if c.exec != nil {
		return c.exec(ctx, query)
	}
	return driver.RowsAffected(0), nil
}

type boundFakeTx struct{}

func (boundFakeTx) Commit() error   { return nil }
func (boundFakeTx) Rollback() error { return nil }

type boundFakeConnector struct {
	recorded *[]string
	exec     func(ctx context.Context, query string) (driver.Result, error)
}

func (c boundFakeConnector) Connect(context.Context) (driver.Conn, error) {
	return &boundFakeConn{recorded: c.recorded, exec: c.exec}, nil
}
func (c boundFakeConnector) Driver() driver.Driver { return boundFakeDriver{} }

type boundFakeDriver struct{}

func (boundFakeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("boundFakeDriver: Open should be unreachable, test uses driver.Connector")
}

// fakeDB returns a *sql.DB over the fakes plus the slice that accumulates every
// statement Reconcile issued, in order.
func fakeDB(t *testing.T, exec func(ctx context.Context, query string) (driver.Result, error)) (*sql.DB, *[]string) {
	t.Helper()
	recorded := &[]string{}
	db := sql.OpenDB(boundFakeConnector{recorded: recorded, exec: exec})
	t.Cleanup(func() { _ = db.Close() })
	return db, recorded
}

func samplePush() codegraph.GraphPush {
	return codegraph.GraphPush{
		Repo:  "mtg-decks",
		Files: []string{"a.py"},
		Entities: []codegraph.Entity{
			{ID: "e1", Name: "f", Type: "function", FilePath: "a.py"},
		},
	}
}

// TestReconcile_TotalDurationIsBounded is the regression test for
// tatara-memory#98. An abandoned transaction on mem-mtg-pg-1 held code-graph
// write locks; every /code-graph:bulk push then blocked inside Reconcile's
// single BeginTx..Commit doing zero work, and the server sent no response
// headers at all until the ingest client's own 900s timeout fired. Nothing on
// the server bounded the transaction end to end: statement_timeout bounds one
// statement and idle_in_transaction_session_timeout only fires on an idle
// session, so a transaction made of many individually-fast statements (or one
// blocked mid-statement, as here) had no ceiling.
func TestReconcile_TotalDurationIsBounded(t *testing.T) {
	// Blocks until the statement's own context is cancelled, which is how a
	// real driver behaves when a backend is stuck waiting for a lock: pgx
	// cancels the in-flight query when the context it was handed is done.
	// Absent a derived deadline there is no such cancellation and this never
	// returns - which is precisely the incident.
	db, _ := fakeDB(t, func(ctx context.Context, _ string) (driver.Result, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	store := codegraph.NewPGStore(db, codegraph.WithReconcileTimeout(80*time.Millisecond))

	start := time.Now()
	_, err := store.Reconcile(context.Background(), samplePush())
	elapsed := time.Since(start)

	require.Error(t, err)
	require.ErrorIs(t, err, codegraph.ErrReconcileTimeout,
		"a reconcile that blows its budget must be classified, not surface as an opaque error")
	require.Less(t, elapsed, 2*time.Second,
		"Reconcile must return on its own budget rather than blocking until the caller gives up")
}

// TestReconcile_TimeoutIsNotReportedAsTxDone pins the classification at the
// commit boundary. database/sql's awaitDone goroutine races Tx.Commit: when it
// wins the done CAS first, Commit returns sql.ErrTxDone rather than the context
// error. Unmapped, that reaches errmap's default arm as a 500 and a plain
// timeout starts looking like a code bug.
func TestReconcile_TimeoutIsNotReportedAsTxDone(t *testing.T) {
	db, _ := fakeDB(t, func(ctx context.Context, query string) (driver.Result, error) {
		if strings.HasPrefix(query, "SET LOCAL") {
			return driver.RowsAffected(0), nil
		}
		time.Sleep(60 * time.Millisecond)
		return driver.RowsAffected(0), nil
	})
	store := codegraph.NewPGStore(db, codegraph.WithReconcileTimeout(70*time.Millisecond))

	_, err := store.Reconcile(context.Background(), samplePush())

	require.Error(t, err)
	require.ErrorIs(t, err, codegraph.ErrReconcileTimeout)
	require.NotErrorIs(t, err, sql.ErrTxDone,
		"a blown budget must not leak sql.ErrTxDone, which errmap would render as a 500")
}

// TestReconcile_SetsLocalLockTimeout proves the lock bound is scoped to
// Reconcile's own transaction with SET LOCAL, not set session-wide.
//
// Session-wide is not an option: pgConnConfig's RuntimeParams apply to the one
// shared *sql.DB that also runs codegraph.Migrate, whose ALTER TABLE statements
// (migrations 0005/0006) take ACCESS EXCLUSIVE on the very tables Reconcile
// holds ROW EXCLUSIVE on for its whole 50-194s. Today the migration waits and
// wins; under a session lock_timeout it would fail with 55P03 and abort
// startup, turning every rolling upgrade that overlaps a push into a
// CrashLoopBackOff. SET LOCAL confines the bound to this transaction and leaves
// migrate, ingest, the memory service and the analytics worker untouched.
func TestReconcile_SetsLocalLockTimeout(t *testing.T) {
	db, recorded := fakeDB(t, nil)
	store := codegraph.NewPGStore(db, codegraph.WithLockTimeout(90*time.Second))

	_, err := store.Reconcile(context.Background(), samplePush())
	require.NoError(t, err)

	require.NotEmpty(t, *recorded)
	first := (*recorded)[0]
	require.Contains(t, first, "SET LOCAL lock_timeout",
		"the lock bound must be the transaction's first statement, and must be SET LOCAL")
	require.Contains(t, first, "90000",
		"lock_timeout is set in milliseconds")
	for _, q := range (*recorded)[1:] {
		require.NotContains(t, q, "SET LOCAL lock_timeout", "set exactly once per transaction")
	}
}

// TestReconcile_SubMillisecondLockTimeoutIsNotDisabled guards a silent
// inversion: Postgres reads lock_timeout = 0 as "wait forever", so naively
// rendering a sub-millisecond duration through Milliseconds() would turn the
// tightest possible bound an operator can ask for into no bound at all -
// producing exactly the unbounded wait this change exists to remove.
func TestReconcile_SubMillisecondLockTimeoutIsNotDisabled(t *testing.T) {
	db, recorded := fakeDB(t, nil)
	store := codegraph.NewPGStore(db, codegraph.WithLockTimeout(500*time.Microsecond))

	_, err := store.Reconcile(context.Background(), samplePush())
	require.NoError(t, err)

	require.NotEmpty(t, *recorded)
	require.Contains(t, (*recorded)[0], "SET LOCAL lock_timeout = 1",
		"a positive duration below 1ms must clamp up to 1ms, never down to 0 (which disables it)")
}

// TestReconcile_NoLockTimeoutWhenDisabled keeps 0 meaning "leave the server
// default alone", so an operator can opt out without a rebuild.
func TestReconcile_NoLockTimeoutWhenDisabled(t *testing.T) {
	db, recorded := fakeDB(t, nil)
	store := codegraph.NewPGStore(db, codegraph.WithLockTimeout(0))

	_, err := store.Reconcile(context.Background(), samplePush())
	require.NoError(t, err)

	for _, q := range *recorded {
		require.NotContains(t, q, "lock_timeout")
	}
}

// TestReconcile_ClassifiesLockTimeout is the diagnostic half of the fix. It
// took seven hours to establish that #98 was lock blocking rather than a dead
// process, a saturated pool or an unhealthy dependency. SQLSTATE 55P03 says so
// in one error, and only fires while waiting for a lock - unlike
// statement_timeout (57014), which cannot tell a lock wait from real work.
func TestReconcile_ClassifiesLockTimeout(t *testing.T) {
	db, _ := fakeDB(t, func(ctx context.Context, query string) (driver.Result, error) {
		if strings.HasPrefix(query, "SET LOCAL") {
			return driver.RowsAffected(0), nil
		}
		return nil, &pgconn.PgError{Code: "55P03", Message: "canceling statement due to lock timeout"}
	})
	store := codegraph.NewPGStore(db, codegraph.WithLockTimeout(30*time.Second))

	_, err := store.Reconcile(context.Background(), samplePush())

	require.ErrorIs(t, err, codegraph.ErrLockTimeout)
	require.NotErrorIs(t, err, codegraph.ErrReconcileTimeout)
}

// TestReconcile_ClassifiesDeadlock covers the other SQLSTATE this transaction
// can realistically hit. Reconcile and the analytics recompute both lock
// code_entities rows and then repo_analytics_state, in no agreed order, so
// Postgres can pick one as a deadlock victim (40P01). Unclassified it is a 500
// on a condition the caller should simply retry.
func TestReconcile_ClassifiesDeadlock(t *testing.T) {
	db, _ := fakeDB(t, func(ctx context.Context, query string) (driver.Result, error) {
		if strings.HasPrefix(query, "SET LOCAL") {
			return driver.RowsAffected(0), nil
		}
		return nil, &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}
	})
	store := codegraph.NewPGStore(db)

	_, err := store.Reconcile(context.Background(), samplePush())

	require.ErrorIs(t, err, codegraph.ErrDeadlock)
}

// TestReconcile_UnrelatedErrorsStayUnclassified guards against the classifier
// swallowing genuine faults into a retryable bucket.
func TestReconcile_UnrelatedErrorsStayUnclassified(t *testing.T) {
	boom := errors.New("relation does not exist")
	db, _ := fakeDB(t, func(ctx context.Context, query string) (driver.Result, error) {
		if strings.HasPrefix(query, "SET LOCAL") {
			return driver.RowsAffected(0), nil
		}
		return nil, boom
	})
	store := codegraph.NewPGStore(db)

	_, err := store.Reconcile(context.Background(), samplePush())

	require.ErrorIs(t, err, boom)
	require.NotErrorIs(t, err, codegraph.ErrLockTimeout)
	require.NotErrorIs(t, err, codegraph.ErrDeadlock)
	require.NotErrorIs(t, err, codegraph.ErrReconcileTimeout)
}

// TestReconcile_UnboundedByDefault keeps every existing NewPGStore(db) call
// site behaving exactly as before, so the bound is opt-in at the wiring layer.
func TestReconcile_UnboundedByDefault(t *testing.T) {
	db, recorded := fakeDB(t, nil)
	store := codegraph.NewPGStore(db)

	_, err := store.Reconcile(context.Background(), samplePush())
	require.NoError(t, err)

	for _, q := range *recorded {
		require.NotContains(t, q, "lock_timeout")
	}
}
