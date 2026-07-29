package httpapi_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
	"github.com/szymonrychu/tatara-memory/internal/httpapi"
)

// failingCodeGraph.Push always returns err. Only Push is exercised here; every
// other CodeGraphService method is the embedded nil interface.
type failingCodeGraph struct {
	httpapi.CodeGraphService
	err error
}

func (f *failingCodeGraph) Push(context.Context, codegraph.GraphPush) (codegraph.PushResult, error) {
	return codegraph.PushResult{}, f.err
}

func postBulk(t *testing.T, err error) *http.Response {
	t.Helper()
	router := httpapi.NewRouter(httpapi.Config{
		Service:   &stubService{},
		CodeGraph: &failingCodeGraph{err: err},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/code-graph:bulk",
		bytes.NewReader([]byte(`{"repo":"mtg-decks","files":["a.py"]}`)))
	router.ServeHTTP(rec, req)
	return rec.Result()
}

// TestCodeGraphBulk_LockContentionIsA503 pins the status the three new
// reconcile failure modes must produce.
//
// 503, deliberately, not the 429 that errmap's pre-existing
// context.DeadlineExceeded arm would give them. That arm exists for saturation,
// and 429 is explicitly excluded from the MemoryHigh5xx rule
// (charts/tatara-memory/templates/prometheusrule.yaml) so shed load does not
// page anyone. Routing a wedged lock there would mean: pushes fail, entities
// upserted goes flat, and nothing pages - which is exactly how
// tatara-memory#98 stayed invisible for seven hours. A reconcile that cannot
// get its locks is a server-side fault worth waking someone for, so it belongs
// on the 5xx path, with Retry-After so a well-behaved client still backs off.
func TestCodeGraphBulk_LockContentionIsA503(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"lock timeout", fmt.Errorf("reconcile: %w", codegraph.ErrLockTimeout)},
		{"deadlock victim", fmt.Errorf("reconcile: %w", codegraph.ErrDeadlock)},
		{"reconcile budget exhausted", fmt.Errorf("reconcile: %w", codegraph.ErrReconcileTimeout)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := postBulk(t, tc.err)
			defer func() { _ = resp.Body.Close() }()

			require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode,
				"must land on the 5xx alert path, not be shed as 429")
			require.NotEmpty(t, resp.Header.Get("Retry-After"),
				"the condition is retryable, so the client must be told to back off")
		})
	}
}

// TestCodeGraphBulk_UnclassifiedErrorStillA500 is the negative control: the
// three new cases must not have widened the retryable bucket.
func TestCodeGraphBulk_UnclassifiedErrorStillA500(t *testing.T) {
	resp := postBulk(t, fmt.Errorf("relation \"code_entities\" does not exist"))
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}
