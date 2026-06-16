package httpapi

// Tests for obs-scaffold round-3 finding 5 in internal/httpapi.
// Finding 5: context.Canceled must map to 499 (not 500) so client disconnects
// don't inflate the server error rate on dashboards.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMapServiceError_ContextCanceled_Returns499(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	mapServiceError(w, r, context.Canceled)
	require.Equal(t, 499, w.Code,
		"context.Canceled must map to 499 (client closed request), not 500 (finding 5)")
}

func TestMapServiceError_ContextDeadlineExceeded_Returns503(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	mapServiceError(w, r, context.DeadlineExceeded)
	require.Equal(t, http.StatusServiceUnavailable, w.Code,
		"context.DeadlineExceeded must still map to 503 (unchanged by finding 5 fix)")
}
