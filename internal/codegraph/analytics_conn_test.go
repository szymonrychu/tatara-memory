package codegraph_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

// The stub driver below implements just enough of database/sql/driver for
// database/sql's own connection-pool and transaction bookkeeping to run for
// real against RecomputeAnalytics, without a live Postgres. That is the point:
// the assertions in this file are all about pool accounting (db.Stats().InUse),
// which only means anything when the real database/sql pool is in the loop.
//
// Regression target: tatara-memory#89. RecomputeAnalytics used to open its
// transaction before labelling communities, so a pool connection - and its
// server-side Postgres backend - was pinned for the duration of one outbound
// LLM call per community (internal/codegraph/openai.go). On mtg-decks that was
// 126 sequential network round-trips inside one open transaction.

// stubDB is the shared, per-test control surface for the stub driver.
type stubDB struct {
	mu sync.Mutex
	// failOn maps a SQL fragment to the error the stub returns instead of
	// running that statement. Fragments are unique per statement in
	// RecomputeAnalytics.
	failOn map[string]error
	// failBegin, when set, fails BeginTx.
	failBegin error
	// failCommit, when set, fails the driver's Commit.
	failCommit error

	entities [][2]string
	edges    [][2]string
}

// newStubDB seeds two dense triangles bridged by c-d, which analytics.Compute
// resolves into exactly two communities (mirrors internal/analytics fixtures).
func newStubDB() *stubDB {
	return &stubDB{
		failOn: map[string]error{},
		entities: [][2]string{
			{"a", "A"}, {"b", "B"}, {"c", "C"},
			{"d", "D"}, {"e", "E"}, {"f", "F"},
		},
		edges: [][2]string{
			{"a", "b"}, {"b", "c"}, {"a", "c"},
			{"d", "e"}, {"e", "f"}, {"d", "f"},
			{"c", "d"},
		},
	}
}

func (s *stubDB) errFor(q string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for frag, err := range s.failOn {
		if strings.Contains(q, frag) {
			return err
		}
	}
	return nil
}

type stubConnector struct{ s *stubDB }

func (c stubConnector) Connect(context.Context) (driver.Conn, error) { return stubConn(c), nil }
func (c stubConnector) Driver() driver.Driver                        { return stubDriver{} }

type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("stubDriver: Open is unreachable, tests use driver.Connector")
}

type stubConn struct{ s *stubDB }

func (stubConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("stubConn: Prepare unsupported (QueryerContext/ExecerContext are used)")
}
func (stubConn) Close() error { return nil }

// CheckNamedValue accepts every argument as-is. RecomputeAnalytics passes
// []string/[]int/[]float64 to the unnest-based batch UPDATE, which pgx handles
// natively but database/sql's default converter rejects.
func (stubConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (c stubConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c stubConn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.s.failBegin != nil {
		return nil, c.s.failBegin
	}
	return stubTx(c), nil
}

func (c stubConn) QueryContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.s.errFor(q); err != nil {
		return nil, err
	}
	switch {
	case strings.Contains(q, "COALESCE(reconciled_at"):
		return &stubRows{
			cols: []string{"reconciled_at"},
			data: [][]driver.Value{{time.Unix(0, 0).UTC()}},
		}, nil
	case strings.Contains(q, "SELECT id, name FROM code_entities"):
		return &stubRows{cols: []string{"id", "name"}, data: pairRows(c.s.entities)}, nil
	case strings.Contains(q, "SELECT from_id, to_id FROM code_edges"):
		return &stubRows{cols: []string{"from_id", "to_id"}, data: pairRows(c.s.edges)}, nil
	}
	return nil, fmt.Errorf("stubConn: unexpected query %q", q)
}

func (c stubConn) ExecContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.s.errFor(q); err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

func pairRows(pairs [][2]string) [][]driver.Value {
	out := make([][]driver.Value, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, []driver.Value{p[0], p[1]})
	}
	return out
}

type stubTx struct{ s *stubDB }

func (t stubTx) Commit() error { return t.s.failCommit }
func (stubTx) Rollback() error { return nil }

type stubRows struct {
	cols []string
	data [][]driver.Value
	i    int
}

func (r *stubRows) Columns() []string { return r.cols }
func (r *stubRows) Close() error      { return nil }
func (r *stubRows) Next(dest []driver.Value) error {
	if r.i >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.i])
	r.i++
	return nil
}

// openStubDB caps the pool at ONE connection. That is deliberate: with a single
// slot, "is a connection held right now" becomes directly observable both as
// db.Stats().InUse and as whether an unrelated caller can acquire the pool at
// all - which is precisely the production failure in #89 (a 10-slot pool shared
// by every caller in the process).
func openStubDB(t *testing.T, s *stubDB) *sql.DB {
	t.Helper()
	db := sql.OpenDB(stubConnector{s: s})
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	return db
}

// recordingLabeler runs fn on every Label call, so a test can observe pool
// state at exactly the moment the (in production: outbound HTTP) call happens.
type recordingLabeler struct {
	fn func(ctx context.Context) (string, error)
}

func (l *recordingLabeler) Label(ctx context.Context, _ []string) (string, error) {
	return l.fn(ctx)
}

// requirePoolDrained waits (briefly) for InUse to fall back to zero. database/sql
// can release a connection from its own Tx.awaitDone goroutine rather than the
// caller's, and that goroutine is not happens-before ordered against the
// RecomputeAnalytics return, so this polls rather than sampling once.
func requirePoolDrained(t *testing.T, db *sql.DB) {
	t.Helper()
	require.Eventually(t, func() bool { return db.Stats().InUse == 0 }, 2*time.Second, 5*time.Millisecond,
		"pool connection was never returned: InUse=%d open=%d", db.Stats().InUse, db.Stats().OpenConnections)

	// Stronger than the counter: prove the slot is genuinely reusable by an
	// unrelated caller. A leaked backend shows up here as a hang, which is what
	// starved /readyz and /memories:bulk in the incident.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, db.PingContext(ctx), "the pool's only slot could not be acquired after the recompute returned")
}

// TestRecomputeAnalytics_LabelsWithoutHoldingAPoolConnection is the direct
// regression test for #89: community labelling must NOT run inside the
// transaction. Each Label call in production is an unbounded outbound HTTP call
// (OpenAILabeler), and doing it with a transaction open pins one pool
// connection - and one Postgres backend - for the whole call.
func TestRecomputeAnalytics_LabelsWithoutHoldingAPoolConnection(t *testing.T) {
	sdb := newStubDB()
	db := openStubDB(t, sdb)
	store := codegraph.NewPGStore(db)

	var inUse []int
	labeler := &recordingLabeler{fn: func(ctx context.Context) (string, error) {
		inUse = append(inUse, db.Stats().InUse)
		// An unrelated caller must be able to use the pool while a label is
		// being fetched. Before the fix this blocks until the 250ms deadline.
		pingCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		if err := db.PingContext(pingCtx); err != nil {
			return "", fmt.Errorf("pool unavailable during labeling: %w", err)
		}
		return "labelled", nil
	}}

	res, err := store.RecomputeAnalytics(context.Background(), "repo/a", labeler, 0)
	require.NoError(t, err)
	require.Equal(t, 2, res.Communities)

	require.Len(t, inUse, 2, "expected one Label call per community")
	for i, n := range inUse {
		require.Zero(t, n,
			"Label call %d ran while the recompute held %d pool connection(s); labelling must happen outside the transaction (tatara-memory#89)", i, n)
	}
	requirePoolDrained(t, db)
}

// TestRecomputeAnalytics_ReturnsConnectionOnEveryFailurePath walks every
// statement RecomputeAnalytics issues and fails it, asserting the pool slot
// comes back each time. Error paths are where a connection leak hides.
func TestRecomputeAnalytics_ReturnsConnectionOnEveryFailurePath(t *testing.T) {
	boom := errors.New("boom")

	cases := []struct {
		name  string
		setup func(*stubDB)
	}{
		{"snapshot select", func(s *stubDB) { s.failOn["COALESCE(reconciled_at"] = boom }},
		{"load entity ids", func(s *stubDB) { s.failOn["SELECT id, name FROM code_entities"] = boom }},
		{"load edge pairs", func(s *stubDB) { s.failOn["SELECT from_id, to_id FROM code_edges"] = boom }},
		{"begin transaction", func(s *stubDB) { s.failBegin = boom }},
		{"node signal update", func(s *stubDB) { s.failOn["UPDATE code_entities"] = boom }},
		{"delete communities", func(s *stubDB) { s.failOn["DELETE FROM code_communities"] = boom }},
		{"insert community", func(s *stubDB) { s.failOn["INSERT INTO code_communities"] = boom }},
		{"clear dirty flag", func(s *stubDB) { s.failOn["UPDATE repo_analytics_state"] = boom }},
		{"commit", func(s *stubDB) { s.failCommit = boom }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sdb := newStubDB()
			tc.setup(sdb)
			db := openStubDB(t, sdb)
			store := codegraph.NewPGStore(db)

			_, err := store.RecomputeAnalytics(context.Background(), "repo/a", nil, 0)
			require.Error(t, err, "the injected failure must surface, not be swallowed")
			requirePoolDrained(t, db)
		})
	}
}

// TestRecomputeAnalytics_ReturnsConnectionWhenContextCancelledDuringLabeling
// covers the cancellation path: labelling is the long, network-bound phase, so
// it is the phase most likely to be cut short by shutdown or a per-run
// deadline. The pool slot must come back regardless.
func TestRecomputeAnalytics_ReturnsConnectionWhenContextCancelledDuringLabeling(t *testing.T) {
	sdb := newStubDB()
	db := openStubDB(t, sdb)
	store := codegraph.NewPGStore(db)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	labeler := &recordingLabeler{fn: func(context.Context) (string, error) {
		cancel()
		return "", errors.New("cancelled")
	}}

	_, err := store.RecomputeAnalytics(ctx, "repo/a", labeler, 0)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	requirePoolDrained(t, db)
}
