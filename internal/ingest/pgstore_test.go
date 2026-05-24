//go:build integration

package ingest_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func openPG(t *testing.T) *sql.DB {
	dsn := os.Getenv("TATARA_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TATARA_TEST_PG_DSN not set")
	}
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	require.NoError(t, db.PingContext(context.Background()))
	return db
}

func TestPGStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openPG(t)
	defer db.Close()
	require.NoError(t, ingest.Migrate(ctx, db))

	store := ingest.NewPGStore(db)
	job := memory.IngestJob{ID: "pgjob1", Status: memory.JobStatusQueued, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, store.CreateJob(ctx, job, []memory.IngestItem{{IdempotencyKey: "k", Text: "a"}}))

	got, err := store.GetJob(ctx, "pgjob1")
	require.NoError(t, err)
	require.Equal(t, memory.JobStatusQueued, got.Status)

	item, ok, err := store.ClaimNextItem(ctx, "pgjob1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "k", item.IdempotencyKey)

	require.NoError(t, store.MarkItemDone(ctx, "pgjob1", "k", nil))
}
