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
	case errors.Is(err, memory.ErrBusy), errors.Is(err, context.DeadlineExceeded), isDBLockContention(err):
		// Backpressure, not failure: either an upstream told us it is busy, or
		// one of our own deadlines fired because the service is saturated
		// (e.g. CreateJob waiting on an exhausted DB pool). The request can
		// succeed if the caller comes back later, and counting it as a server
		// error is what made every concurrent-ingest burst look like an outage
		// on the 5xx-ratio alert (tatara-memory#79/#80/#82/#87). 429 is the
		// status that means exactly this.
		w.Header().Set("Retry-After", "5")
		WriteError(w, http.StatusTooManyRequests, "overloaded, retry after backoff", reqID)
	case errors.Is(err, memory.ErrTransient):
		// Genuine unavailability: the upstream returned 5xx or was unreachable.
		// 503 is reserved for this - a real server-side fault worth alerting on.
		w.Header().Set("Retry-After", "5")
		WriteError(w, http.StatusServiceUnavailable, "upstream temporarily unavailable", reqID)
	case isDBUnavailable(err):
		// The same class as ErrTransient, arriving without a domain wrapper: the
		// code-graph store hands pgx errors back verbatim, so a Postgres that is
		// down, restarting or out of connection slots used to surface as a 500
		// "internal error" - non-retryable to the caller and indistinguishable
		// from a bug on the 5xx-ratio alert (tatara-memory#102).
		loggerFromContext(r.Context()).WarnContext(r.Context(), "database unavailable",
			"request_id", reqID,
			"error", err,
		)
		w.Header().Set("Retry-After", "5")
		WriteError(w, http.StatusServiceUnavailable, "database temporarily unavailable", reqID)
	case errors.Is(err, context.Canceled):
		// Client disconnected mid-request (499 Client Closed Request). This is
		// not a server error; writing 499 keeps it off the 5xx dashboards and
		// prevents inflating the error rate with non-actionable client drops.
		WriteError(w, 499, "client closed request", reqID)
	case errors.Is(err, memory.ErrUpstream):
		loggerFromContext(r.Context()).ErrorContext(r.Context(), "upstream error",
			"request_id", reqID,
			"error", err,
		)
		WriteError(w, http.StatusBadGateway, "upstream error", reqID)
	case errors.Is(err, ingest.ErrDuplicateKey):
		// Duplicate idempotency key is a permanent client error (identical content
		// always produces the same key); 400 prevents retries.
		WriteError(w, http.StatusBadRequest, "duplicate idempotency key in batch", reqID)
	default:
		loggerFromContext(r.Context()).ErrorContext(r.Context(), "internal server error",
			"request_id", reqID,
			"error", err,
		)
		WriteError(w, http.StatusInternalServerError, "internal error", reqID)
	}
}
