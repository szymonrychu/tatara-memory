package httpapi

import (
	"context"
	"errors"
	"net/http"
)

// mapServiceError maps domain errors to HTTP status codes and writes the error envelope.
func mapServiceError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := RequestIDFromContext(r.Context())
	switch {
	case errors.Is(err, ErrNotFound):
		WriteError(w, http.StatusNotFound, "not found", reqID)
	case errors.Is(err, ErrTransient), errors.Is(err, context.DeadlineExceeded):
		w.Header().Set("Retry-After", "5")
		WriteError(w, http.StatusServiceUnavailable, "upstream temporarily unavailable", reqID)
	case errors.Is(err, ErrUpstream):
		WriteError(w, http.StatusBadGateway, "upstream error", reqID)
	default:
		WriteError(w, http.StatusInternalServerError, "internal error", reqID)
	}
}
