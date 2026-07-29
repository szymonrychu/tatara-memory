//go:build integration

package codegraph_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

// TestReconcile_LockTimeoutAgainstRealPostgres reproduces tatara-memory#98 in
// miniature and is the only test that can prove the two halves of the fix that
// a fake driver cannot reach:
//
//  1. that SET LOCAL lock_timeout issued through pgx's extended query protocol
//     actually takes effect on the server (protocol-level Parse accepts utility
//     statements, unlike the SQL-level PREPARE command - this pins that), and
//  2. that the resulting SQLSTATE 55P03 survives the database/sql wrapping as a
//     *pgconn.PgError, which is what classifyReconcileError matches on.
//
// The setup is the incident: one transaction holds a lock on a code_entities
// row for this repo and never commits, exactly like the backend that sat
// state=active on mem-mtg-pg-1 for seven hours. Before this change a push in
// that state blocked with no ceiling and the server sent no response at all.
//
// Requires TATARA_TEST_PG_DSN and -tags integration; skipped otherwise.
func TestReconcile_LockTimeoutAgainstRealPostgres(t *testing.T) {
	store, db, ctx := freshStoreWithDB(t)

	push := codegraph.GraphPush{
		Repo:  "mtg-decks",
		Files: []string{"a.py"},
		Entities: []codegraph.Entity{
			{ID: "e1", Name: "f", Type: "function", FilePath: "a.py"},
		},
	}
	_, err := store.Reconcile(ctx, push)
	require.NoError(t, err, "seed the row the blocker will hold")

	// The abandoned transaction. Never committed, rolled back only by cleanup.
	blocker, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback() }()
	_, err = blocker.ExecContext(ctx, `SELECT 1 FROM code_entities WHERE repo=$1 FOR UPDATE`, push.Repo)
	require.NoError(t, err)

	bounded := codegraph.NewPGStore(db,
		codegraph.WithLockTimeout(500*time.Millisecond),
		codegraph.WithReconcileTimeout(30*time.Second))

	start := time.Now()
	_, err = bounded.Reconcile(ctx, push)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, codegraph.ErrLockTimeout,
		"a push blocked on a held lock must fail as 55P03, not as an opaque error or a timeout")
	require.NotErrorIs(t, err, codegraph.ErrReconcileTimeout,
		"the lock bound must fire well before the reconcile budget, so the failure names its cause")
	require.Less(t, elapsed, 10*time.Second,
		"the point of the lock bound is to fail fast rather than hold the connection")
}

// TestReconcile_SucceedsUncontendedWithBoundsSet is the negative control: the
// same bounds must be invisible when nothing is holding a lock, so a passing
// test above cannot be explained by the bounds simply breaking every push.
func TestReconcile_SucceedsUncontendedWithBoundsSet(t *testing.T) {
	_, db, ctx := freshStoreWithDB(t)

	bounded := codegraph.NewPGStore(db,
		codegraph.WithLockTimeout(500*time.Millisecond),
		codegraph.WithReconcileTimeout(30*time.Second))

	res, err := bounded.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "mtg-decks",
		Files: []string{"a.py"},
		Entities: []codegraph.Entity{
			{ID: "e1", Name: "f", Type: "function", FilePath: "a.py"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, res.EntitiesUpserted)
}
