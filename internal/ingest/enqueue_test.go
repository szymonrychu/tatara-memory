package ingest_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestEnqueueAssignsKeys(t *testing.T) {
	ctx := context.Background()
	s := ingest.NewMemStore()
	e := ingest.NewEnqueuer(s)

	items := []memory.IngestItem{{Text: "a"}, {IdempotencyKey: "given", Text: "b"}}
	job, err := e.Enqueue(ctx, items)
	require.NoError(t, err)
	require.Equal(t, memory.JobStatusQueued, job.Status)
	require.Equal(t, 2, job.Total)

	got, err := s.GetJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, job.ID, got.ID)
}

func TestEnqueueEmpty(t *testing.T) {
	ctx := context.Background()
	e := ingest.NewEnqueuer(ingest.NewMemStore())
	_, err := e.Enqueue(ctx, nil)
	require.Error(t, err)
}

func TestEnqueueRejectsDuplicateBatchKey(t *testing.T) {
	ctx := context.Background()
	store := ingest.NewMemStore()
	e := ingest.NewEnqueuer(store)

	items := []memory.IngestItem{{IdempotencyKey: "k1", Text: "a"}, {IdempotencyKey: "k1", Text: "b"}}
	_, err := e.Enqueue(ctx, items)
	require.ErrorIs(t, err, ingest.ErrDuplicateKey)
}
