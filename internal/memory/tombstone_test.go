//go:build integration

package memory_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TATARA_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TATARA_TEST_PG_DSN not set")
	}
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, db.PingContext(ctx))
	require.NoError(t, memory.Migrate(ctx, db))
	_, err = db.ExecContext(ctx, `DELETE FROM deleted_memories`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestTombstoneStore_MarkAndCheck(t *testing.T) {
	db := openTestDB(t)
	s := memory.NewTombstoneStore(db)
	ctx := context.Background()

	require.NoError(t, memory.Migrate(ctx, db))

	deleted, err := s.IsDeleted(ctx, "abc")
	require.NoError(t, err)
	require.False(t, deleted)

	require.NoError(t, s.Mark(ctx, "abc"))

	deleted, err = s.IsDeleted(ctx, "abc")
	require.NoError(t, err)
	require.True(t, deleted)
}

func TestTombstoneStore_ListOlderThan(t *testing.T) {
	db := openTestDB(t)
	s := memory.NewTombstoneStore(db)
	ctx := context.Background()

	require.NoError(t, memory.Migrate(ctx, db))

	require.NoError(t, s.Mark(ctx, "old"))
	require.NoError(t, s.Mark(ctx, "fresh"))
	_, err := db.ExecContext(ctx, `UPDATE deleted_memories SET deleted_at = now() - interval '25 hours' WHERE track_id = 'old'`)
	require.NoError(t, err)

	aged, err := s.ListOlderThan(ctx, 24*time.Hour, 10)
	require.NoError(t, err)
	require.Len(t, aged, 1, "only the >24h tombstone is aged; fresh is excluded")
	require.Equal(t, "old", aged[0].TrackID)
	require.Equal(t, 0, aged[0].Attempts, "a freshly-aged tombstone has never been force-checked")
}

func TestTombstoneStore_ListOlderThan_ExcludesBackedOffCandidates(t *testing.T) {
	db := openTestDB(t)
	s := memory.NewTombstoneStore(db)
	ctx := context.Background()

	require.NoError(t, memory.Migrate(ctx, db))

	require.NoError(t, s.Mark(ctx, "old"))
	_, err := db.ExecContext(ctx, `UPDATE deleted_memories SET deleted_at = now() - interval '25 hours' WHERE track_id = 'old'`)
	require.NoError(t, err)

	// Not yet due: excluded even though it is aged.
	require.NoError(t, s.RecordForceCheckStillPresent(ctx, "old", time.Now().Add(2*time.Hour)))
	aged, err := s.ListOlderThan(ctx, 24*time.Hour, 10)
	require.NoError(t, err)
	require.Empty(t, aged, "a backed-off tombstone must not be a force-reap candidate before next_force_check_at")

	// Due: included again once next_force_check_at has elapsed, with the bumped attempt count.
	require.NoError(t, s.RecordForceCheckStillPresent(ctx, "old", time.Now().Add(-time.Minute)))
	aged, err = s.ListOlderThan(ctx, 24*time.Hour, 10)
	require.NoError(t, err)
	require.Len(t, aged, 1)
	require.Equal(t, "old", aged[0].TrackID)
	require.Equal(t, 2, aged[0].Attempts)
}

func TestTombstoneStore_List(t *testing.T) {
	db := openTestDB(t)
	s := memory.NewTombstoneStore(db)
	ctx := context.Background()

	require.NoError(t, memory.Migrate(ctx, db))

	require.NoError(t, s.Mark(ctx, "a"))
	require.NoError(t, s.Mark(ctx, "b"))
	ids, err := s.List(ctx, 10)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"a", "b"}, ids)
}

func TestTombstoneStore_Delete(t *testing.T) {
	db := openTestDB(t)
	s := memory.NewTombstoneStore(db)
	ctx := context.Background()

	require.NoError(t, memory.Migrate(ctx, db))

	require.NoError(t, s.Mark(ctx, "x"))
	require.NoError(t, s.Delete(ctx, "x"))
	deleted, err := s.IsDeleted(ctx, "x")
	require.NoError(t, err)
	require.False(t, deleted)
}
