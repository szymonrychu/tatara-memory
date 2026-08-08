package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

// tatara-memory#98: /code-graph:bulk on mem-mtg blocked behind an abandoned
// tatara_memory transaction on mem-mtg-pg-1 that had been open since ~06:05:48Z
// and was still growing 1s per 1s of wall clock 89 minutes later. It held write
// locks on the code-graph tables, so every later code-graph write for mtg-decks
// blocked on them DOING ZERO WORK (code_graph_entities_upserted_total flat, no
// analytics in flight, CPU idle) until the ingester's 900s client timeout fired.
// Three attempts x ~900s, then BackoffLimitExceeded. Reads were unaffected
// throughout at ~2.5ms, because MVCC readers do not block on writers - that
// asymmetry is the signature of lock blocking.
//
// Nothing bounded the wait: there was no lock_timeout in play anywhere. A lock
// wait is not work, so it is the one thing a write should never do
// indefinitely.

// lockingConnector models one Postgres session against a table whose row locks
// another, abandoned session already holds. It honours the session's
// lock_timeout GUC exactly as a real backend does - and, when no lock_timeout
// was negotiated, waits forever, which is the origin/main behaviour.
type lockingConnector struct {
	params   map[string]string
	released <-chan struct{}
}

func (c *lockingConnector) Connect(context.Context) (driver.Conn, error) {
	return &lockingConn{c: c}, nil
}
func (c *lockingConnector) Driver() driver.Driver { return lockingDriver{} }

type lockingDriver struct{}

func (lockingDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("lockingDriver: tests use driver.Connector")
}

type lockingConn struct{ c *lockingConnector }

func (*lockingConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("lockingConn: ExecerContext is used")
}
func (*lockingConn) Close() error                             { return nil }
func (*lockingConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (c *lockingConn) Begin() (driver.Tx, error) { return lockingTx{}, nil }
func (c *lockingConn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return lockingTx{}, nil
}

// lockWait returns the session's lock_timeout as a channel that fires when the
// wait must be abandoned, or nil when the session negotiated no lock_timeout at
// all (wait forever).
func (c *lockingConn) lockWait(t *time.Timer) <-chan time.Time {
	v, ok := c.c.params["lock_timeout"]
	if !ok {
		return nil
	}
	ms, err := strconv.Atoi(v)
	if err != nil || ms <= 0 {
		return nil
	}
	t.Reset(time.Duration(ms) * time.Millisecond)
	return t.C
}

func (c *lockingConn) ExecContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	if !strings.Contains(q, "DELETE FROM code_edges") {
		return driver.RowsAffected(0), nil
	}
	// This is the statement that needs a lock the abandoned transaction holds.
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	timeout := c.lockWait(timer)
	select {
	case <-c.c.released:
		return driver.RowsAffected(0), nil
	case <-timeout:
		return nil, &pgconn.PgError{
			Severity: "ERROR",
			Code:     "55P03", // lock_not_available
			Message:  "canceling statement due to lock timeout",
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *lockingConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return emptyRows{}, nil
}

type lockingTx struct{}

func (lockingTx) Commit() error   { return nil }
func (lockingTx) Rollback() error { return nil }

type emptyRows struct{}

func (emptyRows) Columns() []string         { return nil }
func (emptyRows) Close() error              { return nil }
func (emptyRows) Next([]driver.Value) error { return io.EOF }

// TestReconcile_BlockedOnALockFailsFastInsteadOfHanging is the behavioural
// regression for #98. It drives a real codegraph.PGStore.Reconcile - the whole
// /code-graph:bulk write, one BeginTx held to Commit - against a session
// configured exactly as the process configures its own, and holds the row locks
// for the entire test.
//
// On origin/main PG_LOCK_TIMEOUT does not exist, so no lock_timeout reaches the
// session and this write blocks until the 5s guard below fires: the production
// shape of burning 3x900s in an ingest Job that never gets a byte back.
func TestReconcile_BlockedOnALockFailsFastInsteadOfHanging(t *testing.T) {
	os.Clearenv()
	t.Setenv("PG_LOCK_TIMEOUT", "200ms")
	cfg, err := loadConfig([]string{})
	require.NoError(t, err)

	connCfg, err := pgConnConfig("postgres://u:p@h:5432/d?sslmode=disable", cfg)
	require.NoError(t, err)

	// The abandoned transaction: opened, never committed, never rolled back.
	held := make(chan struct{})
	t.Cleanup(func() { close(held) })

	db := sql.OpenDB(&lockingConnector{params: connCfg.RuntimeParams, released: held})
	t.Cleanup(func() { _ = db.Close() })
	store := codegraph.NewPGStore(db)

	// No client deadline, deliberately: /code-graph:bulk opts out of the server
	// WriteTimeout and its handler sets no deadline of its own, so the only
	// thing that can bound this wait is the database session.
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, rerr := store.Reconcile(context.Background(), codegraph.GraphPush{
			Repo:  "mtg-decks",
			Files: []string{"decks/build.py"},
		})
		done <- rerr
	}()

	select {
	case rerr := <-done:
		require.Error(t, rerr)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, rerr, &pgErr)
		require.Equal(t, "55P03", pgErr.Code,
			"a blocked write must come back as lock_not_available, i.e. the lock wait was bounded")
		require.Less(t, time.Since(start), 5*time.Second)
	case <-time.After(5 * time.Second):
		t.Fatal("the write is still blocked on a lock another session holds; " +
			"nothing bounds a lock wait, which is how /code-graph:bulk burned 3x900s " +
			"behind an abandoned transaction (tatara-memory#98)")
	}
}
