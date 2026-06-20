package memory_test

// TDD: LightRAG returns delete status="busy" when its pipeline lock is held
// (it is mid-ingest). That is a transient condition: the delete should be
// retried, not failed permanently. Mapping it to ErrUpstream (HTTP 502,
// non-retryable) made every reconcile-delete fail whenever LightRAG was busy
// ingesting, hard-failing the whole bulk ingest job. "busy" must map to
// ErrTransient (HTTP 503 + Retry-After) so callers retry.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestDeleteMemory_DeleteDocsBusyIsTransient(t *testing.T) {
	ctx := context.Background()
	inner := fake.New()
	lr := &deletionStatusFakeLR{inner: inner, status: "busy"}
	svc := memory.NewService(lr, nil)

	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "hello"})
	require.NoError(t, err)

	err = svc.DeleteMemory(ctx, m.ID)
	require.Error(t, err, "DeleteMemory must fail when DeleteDocs responds with status=busy")
	require.ErrorIs(t, err, memory.ErrTransient,
		"DeleteDocs status=busy is a transient pipeline-lock condition and must map to ErrTransient (retryable)")
	require.NotErrorIs(t, err, memory.ErrUpstream,
		"busy must NOT be a permanent ErrUpstream")
}
