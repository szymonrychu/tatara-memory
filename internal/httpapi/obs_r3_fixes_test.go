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

// Superseded by tatara-memory#80: a deadline firing is saturation (our own
// bound ran out), which is retryable backpressure, not unavailability, so it is
// now 429 + Retry-After. See TestMapServiceError_BackpressureIs429_
// UnavailabilityIs503 in backpressure_test.go for the full contract.
func TestMapServiceError_ContextDeadlineExceeded_Returns429(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	mapServiceError(w, r, context.DeadlineExceeded)
	require.Equal(t, http.StatusTooManyRequests, w.Code,
		"context.DeadlineExceeded is local saturation: 429, not 500 (finding 5) and no longer 503")
	require.Equal(t, "5", w.Header().Get("Retry-After"))
}
