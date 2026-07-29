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
	case errors.Is(err, codegraph.ErrLockTimeout),
		errors.Is(err, codegraph.ErrDeadlock),
		errors.Is(err, codegraph.ErrReconcileTimeout):
		// The code-graph write transaction could not get, or could not hold,
		// its locks (tatara-memory#98). Retryable, hence Retry-After - but 503
		// rather than the 429 the DeadlineExceeded arm below would produce.
		// 429 means "you offered too much work", and backing off does not clear
		// a transaction someone else abandoned. It is also deliberately
		// invisible to MemoryHigh5xx (charts/tatara-memory/templates/
		// prometheusrule.yaml), so classifying this as shed load would page
		// nobody while every push failed and code_graph_entities_upserted_total
		// sat flat - precisely how #98 ran undetected for seven hours.
		//
		// This arm MUST stay above the DeadlineExceeded case, which would
		// otherwise swallow anything wrapping a context error.
		w.Header().Set("Retry-After", "5")
		loggerFromContext(r.Context()).ErrorContext(r.Context(), "code-graph write path blocked",
			"request_id", reqID,
			"error", err,
		)
		WriteError(w, http.StatusServiceUnavailable, "code-graph write path blocked, retry after backoff", reqID)
	case errors.Is(err, memory.ErrBusy), errors.Is(err, context.DeadlineExceeded):
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
