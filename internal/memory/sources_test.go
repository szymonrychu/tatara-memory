//go:build integration

package memory_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func openSourcesDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TATARA_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TATARA_TEST_PG_DSN not set; skipping integration test")
	}
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	require.NoError(t, memory.Migrate(ctx, db))
	_, err = db.ExecContext(ctx, `DELETE FROM memory_sources`)
	require.NoError(t, err)
	return db
}

func TestSourceStoreAddListDelete(t *testing.T) {
	ctx := context.Background()
	db := openSourcesDB(t)
	ss := memory.NewSourceStore(db)

	require.NoError(t, ss.Add(ctx, "repoA", "a.go", "trk1"))
	require.NoError(t, ss.Add(ctx, "repoA", "a.go", "trk2"))
	require.NoError(t, ss.Add(ctx, "repoA", "b.go", "trk3"))
	// idempotent re-add
	require.NoError(t, ss.Add(ctx, "repoA", "a.go", "trk1"))

	ids, err := ss.TrackIDs(ctx, "repoA", "a.go")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"trk1", "trk2"}, ids)

	n, err := ss.DeleteByFile(ctx, "repoA", "a.go")
	require.NoError(t, err)
	require.Equal(t, int64(2), n)

	ids, err = ss.TrackIDs(ctx, "repoA", "a.go")
	require.NoError(t, err)
	require.Empty(t, ids)

	// b.go untouched
	ids, err = ss.TrackIDs(ctx, "repoA", "b.go")
	require.NoError(t, err)
	require.Equal(t, []string{"trk3"}, ids)
}
