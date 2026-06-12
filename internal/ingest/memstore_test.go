package ingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestMemStoreCreateGet(t *testing.T) {
	ctx := context.Background()
	s := ingest.NewMemStore()

	job := memory.IngestJob{ID: "j1", Status: memory.JobStatusQueued, Total: 2, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	items := []memory.IngestItem{
		{IdempotencyKey: "k1", Text: "a"},
		{IdempotencyKey: "k2", Text: "b"},
	}
	require.NoError(t, s.CreateJob(ctx, job, items))

	got, err := s.GetJob(ctx, "j1")
	require.NoError(t, err)
	require.Equal(t, memory.JobStatusQueued, got.Status)
	require.Equal(t, 2, got.Total)
}

func TestMemStoreItemIdempotent(t *testing.T) {
	ctx := context.Background()
	s := ingest.NewMemStore()
	job := memory.IngestJob{ID: "j2", Status: memory.JobStatusQueued, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, s.CreateJob(ctx, job, []memory.IngestItem{{IdempotencyKey: "dup", Text: "a"}}))

	dupJob := memory.IngestJob{ID: "j2", Status: memory.JobStatusQueued, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	err := s.CreateJob(ctx, dupJob, []memory.IngestItem{{IdempotencyKey: "dup", Text: "a"}})
	require.ErrorIs(t, err, ingest.ErrJobExists)
}

func TestMemStoreRequeueOrphanedItems(t *testing.T) {
	ctx := context.Background()
	s := ingest.NewMemStore()

	// Unfinished job with an item left 'running' by a crashed worker.
	running := memory.IngestJob{ID: "run", Status: memory.JobStatusRunning, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, s.CreateJob(ctx, running, []memory.IngestItem{{IdempotencyKey: "orphan", Text: "x"}}))
	_, ok, err := s.ClaimNextItem(ctx, "run")
	require.NoError(t, err)
	require.True(t, ok) // item is now 'running'

	// Terminal job whose item is also 'running' must be left untouched.
	done := memory.IngestJob{ID: "done", Status: memory.JobStatusSucceeded, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, s.CreateJob(ctx, done, []memory.IngestItem{{IdempotencyKey: "k", Text: "y"}}))
	_, ok, err = s.ClaimNextItem(ctx, "done")
	require.NoError(t, err)
	require.True(t, ok)

	n, err := s.RequeueOrphanedItems(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	// The orphan is claimable again.
	item, ok, err := s.ClaimNextItem(ctx, "run")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "orphan", item.IdempotencyKey)

	// The terminal job's item stayed 'running' (not requeued).
	_, ok, err = s.ClaimNextItem(ctx, "done")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestMemStoreClaimNextItem(t *testing.T) {
	ctx := context.Background()
	s := ingest.NewMemStore()
	job := memory.IngestJob{ID: "j3", Status: memory.JobStatusRunning, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, s.CreateJob(ctx, job, []memory.IngestItem{{IdempotencyKey: "k", Text: "x"}}))

	item, ok, err := s.ClaimNextItem(ctx, "j3")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "k", item.IdempotencyKey)

	_, ok, err = s.ClaimNextItem(ctx, "j3")
	require.NoError(t, err)
	require.False(t, ok)
}
