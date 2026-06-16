package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// mapServiceError maps domain errors to HTTP status codes and writes the error envelope.
func mapServiceError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := RequestIDFromContext(r.Context())
	switch {
	case errors.Is(err, codegraph.ErrEntityNotFound):
		WriteError(w, http.StatusNotFound, "not found", reqID)
	case errors.Is(err, codegraph.ErrInvalidScope):
		WriteError(w, http.StatusBadRequest, err.Error(), reqID)
	case errors.Is(err, memory.ErrInvalid):
		WriteError(w, http.StatusBadRequest, "invalid input", reqID)
	case errors.Is(err, memory.ErrNotFound):
		WriteError(w, http.StatusNotFound, "not found", reqID)
	case errors.Is(err, memory.ErrTransient), errors.Is(err, context.DeadlineExceeded):
		w.Header().Set("Retry-After", "5")
		WriteError(w, http.StatusServiceUnavailable, "upstream temporarily unavailable", reqID)
	case errors.Is(err, memory.ErrUpstream):
		WriteError(w, http.StatusBadGateway, "upstream error", reqID)
	case errors.Is(err, ingest.ErrDuplicateKey):
		// Duplicate idempotency key is a permanent client error (identical content
		// always produces the same key); 400 prevents retries.
		WriteError(w, http.StatusBadRequest, "duplicate idempotency key in batch", reqID)
	default:
		WriteError(w, http.StatusInternalServerError, "internal error", reqID)
	}
}
