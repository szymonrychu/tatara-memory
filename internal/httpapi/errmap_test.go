package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
	"github.com/szymonrychu/tatara-memory/internal/ingest"
)

func TestMapServiceError_CodeGraph(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"entity not found", codegraph.ErrEntityNotFound, http.StatusNotFound},
		{"invalid scope", codegraph.ErrInvalidScope, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			mapServiceError(w, r, c.err)
			require.Equal(t, c.want, w.Code)
		})
	}
}

// TestMapServiceError_DuplicateKey verifies that ErrDuplicateKey maps to 400,
// not 500. A duplicate idempotency key is a permanent client error (same input
// always produces the same key) and must not trigger retries.
func TestMapServiceError_DuplicateKey(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	mapServiceError(w, r, ingest.ErrDuplicateKey)
	require.Equal(t, http.StatusBadRequest, w.Code)
}
