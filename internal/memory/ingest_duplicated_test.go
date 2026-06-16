package memory_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// CreateMemory must treat LightRAG "duplicated" and "partial_success" as
// idempotent successes (re-ingesting unchanged content returns "duplicated"
// with the existing track_id). Treating them as ErrUpstream made every
// re-ingest job fail every item, piling up errored ingest Job pods.
func TestCreateMemory_IdempotentStatusesAreSuccess(t *testing.T) {
	for _, status := range []string{"duplicated", "partial_success"} {
		f := fake.New()
		f.SetInsertResponse(status, "insert_existing_123")
		svc := memory.NewService(f, nil)

		m, err := svc.CreateMemory(context.Background(), memory.Memory{Text: "test"})
		require.NoError(t, err, "status %q must be treated as success", status)
		require.Equal(t, "insert_existing_123", m.ID, "status %q must reuse the returned track_id", status)
	}
}

// A genuine failure status, and an empty track_id, must still error.
func TestCreateMemory_FailureStatusStillErrors(t *testing.T) {
	f := fake.New()
	f.SetInsertResponse("failure", "")
	svc := memory.NewService(f, nil)

	_, err := svc.CreateMemory(context.Background(), memory.Memory{Text: "test"})
	require.ErrorIs(t, err, memory.ErrUpstream)

	f2 := fake.New()
	f2.SetInsertResponse("duplicated", "") // accepted status but unusable empty track_id
	svc2 := memory.NewService(f2, nil)
	_, err2 := svc2.CreateMemory(context.Background(), memory.Memory{Text: "test"})
	require.ErrorIs(t, err2, memory.ErrUpstream)
}
