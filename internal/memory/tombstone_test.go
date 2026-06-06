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

func TestTombstoneStore_Reap(t *testing.T) {
	db := openTestDB(t)
	s := memory.NewTombstoneStore(db)
	ctx := context.Background()

	require.NoError(t, memory.Migrate(ctx, db))

	require.NoError(t, s.Mark(ctx, "old"))
	_, err := db.ExecContext(ctx, `UPDATE deleted_memories SET deleted_at = now() - interval '25 hours' WHERE track_id = 'old'`)
	require.NoError(t, err)

	n, err := s.ReapOlderThan(ctx, 24*time.Hour)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
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
