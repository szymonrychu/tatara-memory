package lightrag_test

// TDD for tatara-memory#90 / #91: the reconcile-purge path shed 82% of
// /memories:bulk as 503 because DeleteDocs gave up on LightRAG's pipeline
// lock after a fixed 200+400+800ms = 1.4s ladder, while a LightRAG document
// delete holds that lock for a measured 6-42s (41.85s worst case, #90 Q9).
// The retry must be bounded by a deadline sized against that distribution,
// not by a fixed attempt count, and exhausting it must be an explicit error
// rather than a busy body handed back as a success.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

// busyUntil serves status="busy" until hold has elapsed, then deletion_started.
// It models LightRAG answering HTTP 200 promptly with a refusal while a
// concurrent pipeline (an insert wave, or a previous delete's graph rebuild)
// owns the lock.
func busyUntil(hold time.Duration, calls *atomic.Int64) http.HandlerFunc {
	start := time.Now()
	return func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		status := "busy"
		if time.Since(start) >= hold {
			status = "deletion_started"
		}
		_ = json.NewEncoder(w).Encode(lightrag.DeleteDocByIdResponse{
			Status: status, Message: "pipeline busy", DocID: "doc-1",
		})
	}
}

func TestHTTPClient_DeleteDocs_WaitsOutLockHeldPastOldFixedBudget(t *testing.T) {
	// A 2.5s lock hold is still an order of magnitude shorter than the 6-42s
	// LightRAG really takes, yet the old 1.4s ladder already sheds it. The
	// delete must wait it out and succeed.
	var calls atomic.Int64
	c, _ := newTestClient(t, busyUntil(2500*time.Millisecond, &calls))

	start := time.Now()
	resp, err := c.DeleteDocs(context.Background(), lightrag.DeleteDocRequest{DocIDs: []string{"doc-1"}})
	elapsed := time.Since(start)

	require.NoError(t, err, "a lock held longer than the old 1.4s budget must not be shed")
	require.Equal(t, "deletion_started", resp.Status)
	require.GreaterOrEqual(t, elapsed, 2500*time.Millisecond, "must actually have waited for the lock")
	require.Less(t, elapsed, 20*time.Second, "must not poll far past the lock release")
	require.Greater(t, calls.Load(), int64(4), "must outlast the old initial+3 attempt ladder")
}

func TestHTTPClient_DeleteDocs_BusyPastBudgetReturnsBusyError(t *testing.T) {
	// When the lock never frees inside the budget the caller must get a real
	// error naming the condition. Previously the still-busy body was returned
	// with a nil error, so the failure was re-derived one layer up and counted
	// as a lightrag_calls_total success.
	var calls atomic.Int64
	c, _ := newTestClient(t, busyUntil(time.Hour, &calls))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	resp, err := c.DeleteDocs(ctx, lightrag.DeleteDocRequest{DocIDs: []string{"doc-1"}})
	elapsed := time.Since(start)

	require.Error(t, err, "an exhausted busy budget must not be reported as a success")
	require.Nil(t, resp)
	var busyErr *lightrag.BusyError
	require.ErrorAs(t, err, &busyErr, "exhaustion must surface as *lightrag.BusyError")
	require.Equal(t, lightrag.OpDeleteDocs, busyErr.Op)
	require.Greater(t, busyErr.Attempts, 1)
	require.Less(t, elapsed, 2*time.Second, "must stop at the caller's deadline, not the default budget")
}

func TestHTTPClient_DeleteDocs_BusyRetryStopsWhenCallerCancels(t *testing.T) {
	// A client disconnect must abort the wait with the context error (499 at
	// the HTTP edge), not be laundered into a BusyError.
	var calls atomic.Int64
	c, _ := newTestClient(t, busyUntil(time.Hour, &calls))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	_, err := c.DeleteDocs(ctx, lightrag.DeleteDocRequest{DocIDs: []string{"doc-1"}})
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled), "caller cancellation must surface as context.Canceled, got %v", err)
}
