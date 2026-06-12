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

	_, _ = db.ExecContext(ctx, `DELETE FROM ingest_job_items WHERE job_id='pgjob1'`)
	_, _ = db.ExecContext(ctx, `DELETE FROM ingest_jobs WHERE id='pgjob1'`)

	store := ingest.NewPGStore(db)
	job := memory.IngestJob{ID: "pgjob1", Status: memory.JobStatusQueued, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, store.CreateJob(ctx, job, []memory.IngestItem{
		{IdempotencyKey: "k", Text: "chunk body", Metadata: map[string]string{"entity": "go:func:Foo"}},
	}))

	got, err := store.GetJob(ctx, "pgjob1")
	require.NoError(t, err)
	require.Equal(t, memory.JobStatusQueued, got.Status)

	item, ok, err := store.ClaimNextItem(ctx, "pgjob1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "k", item.IdempotencyKey)
	// The worker needs the payload to send to LightRAG; the PG store must
	// persist and return Text and Metadata, not just the idempotency key.
	require.Equal(t, "chunk body", item.Text)
	require.Equal(t, map[string]string{"entity": "go:func:Foo"}, item.Metadata)

	require.NoError(t, store.MarkItemDone(ctx, "pgjob1", "k", nil))
}

func TestPGStoreRequeueOrphanedItems(t *testing.T) {
	ctx := context.Background()
	db := openPG(t)
	defer db.Close()
	require.NoError(t, ingest.Migrate(ctx, db))

	_, _ = db.ExecContext(ctx, `DELETE FROM ingest_job_items WHERE job_id IN ('pgrun','pgdone')`)
	_, _ = db.ExecContext(ctx, `DELETE FROM ingest_jobs WHERE id IN ('pgrun','pgdone')`)

	store := ingest.NewPGStore(db)

	// Unfinished job whose only item was left 'running' by a crashed worker.
	require.NoError(t, store.CreateJob(ctx,
		memory.IngestJob{ID: "pgrun", Status: memory.JobStatusRunning, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		[]memory.IngestItem{{IdempotencyKey: "orphan", Text: "x"}}))
	_, ok, err := store.ClaimNextItem(ctx, "pgrun")
	require.NoError(t, err)
	require.True(t, ok) // item is now 'running'

	// Terminal job whose item is also 'running' must be left untouched.
	require.NoError(t, store.CreateJob(ctx,
		memory.IngestJob{ID: "pgdone", Status: memory.JobStatusSucceeded, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		[]memory.IngestItem{{IdempotencyKey: "k", Text: "y"}}))
	_, ok, err = store.ClaimNextItem(ctx, "pgdone")
	require.NoError(t, err)
	require.True(t, ok)

	n, err := store.RequeueOrphanedItems(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	// The orphan is claimable again; the terminal job's item is not.
	item, ok, err := store.ClaimNextItem(ctx, "pgrun")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "orphan", item.IdempotencyKey)

	_, ok, err = store.ClaimNextItem(ctx, "pgdone")
	require.NoError(t, err)
	require.False(t, ok)
}
