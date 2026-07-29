package memory_test

// LightRAG returns delete status="busy" when its pipeline lock is held (it is
// mid-ingest). That is backpressure, not unavailability: the delete should be
// retried, not failed permanently, and the caller should be told to back off
// (HTTP 429 + Retry-After) rather than told the service is down (503).
// Originally this mapped to ErrUpstream (502, non-retryable), then to
// ErrTransient (503); ErrBusy is the classification that carries "retry, we
// are saturated" without inflating the 5xx error ratio (tatara-memory#80).

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestDeleteMemory_DeleteDocsBusyIsBackpressure(t *testing.T) {
	ctx := context.Background()
	inner := fake.New()
	lr := &deletionStatusFakeLR{inner: inner, status: "busy"}
	svc := memory.NewService(lr, nil)

	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "hello"})
	require.NoError(t, err)

	err = svc.DeleteMemory(ctx, m.ID)
	require.Error(t, err, "DeleteMemory must fail when DeleteDocs responds with status=busy")
	require.ErrorIs(t, err, memory.ErrBusy,
		"DeleteDocs status=busy is upstream backpressure and must map to ErrBusy (429 + Retry-After)")
	require.NotErrorIs(t, err, memory.ErrUpstream,
		"busy must NOT be a permanent ErrUpstream")
	require.NotErrorIs(t, err, memory.ErrTransient,
		"busy is backpressure, not genuine unavailability - it must not surface as a 5xx")
}

// A LightRAG 429 is the same signal over HTTP. Before this fix it fell through
// wrapUpstream's default branch to ErrUpstream, turning an explicit "slow down"
// into a permanent 502.
func TestWrapUpstream_HTTP429IsBackpressure(t *testing.T) {
	svc := memory.NewService(&errClient{err: &lightrag.HTTPError{Status: http.StatusTooManyRequests}}, nil)
	_, err := svc.GetMemory(context.Background(), "id")
	require.ErrorIs(t, err, memory.ErrBusy,
		"an upstream 429 is backpressure, not a permanent upstream error")
	require.NotErrorIs(t, err, memory.ErrUpstream)
}

// A timeout on an upstream call under load is backpressure too: the request did
// not fail, it ran out of budget. 503 would page an operator for load.
func TestWrapUpstream_DeadlineExceededIsBackpressure(t *testing.T) {
	svc := memory.NewService(&errClient{err: context.DeadlineExceeded}, nil)
	_, err := svc.GetMemory(context.Background(), "id")
	require.ErrorIs(t, err, memory.ErrBusy)
}

// Genuine upstream unavailability (5xx / transport failure) keeps its 503
// classification - that is what 503 is for.
func TestWrapUpstream_Upstream5xxStaysTransient(t *testing.T) {
	svc := memory.NewService(&errClient{err: &lightrag.HTTPError{Status: http.StatusBadGateway}}, nil)
	_, err := svc.GetMemory(context.Background(), "id")
	require.ErrorIs(t, err, memory.ErrTransient)
	require.NotErrorIs(t, err, memory.ErrBusy)
}
