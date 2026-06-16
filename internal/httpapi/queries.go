package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// maxSmallBody is the body-size cap for small (non-bulk) POST/PATCH endpoints.
// 1 MiB is generous for any structured query payload; bulk endpoints use maxBulkBody.
const maxSmallBody = 1 << 20 // 1 MiB

// maxTopK caps the top_k field on query requests to prevent unbounded LightRAG retrievals.
// A client sending top_k=100000000 would otherwise force LightRAG to attempt a huge retrieval.
const maxTopK = 500

// defaultTopK is applied when top_k is zero (omitted).
const defaultTopK = 10

func decodeStrict(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxSmallBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
		return false
	}
	return true
}

// clampTopK validates and clamps q.TopK before forwarding to the service.
// Returns false and writes a 400 if TopK is negative.
func clampTopK(w http.ResponseWriter, r *http.Request, q *memory.Query) bool {
	if q.TopK < 0 {
		WriteError(w, http.StatusBadRequest, "top_k must be non-negative", RequestIDFromContext(r.Context()))
		return false
	}
	if q.TopK == 0 {
		q.TopK = defaultTopK
	}
	if q.TopK > maxTopK {
		q.TopK = maxTopK
	}
	return true
}

func handlePostQuery(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var q memory.Query
		if !decodeStrict(w, r, &q) {
			return
		}
		if !q.Mode.Valid() {
			WriteError(w, http.StatusBadRequest, "invalid mode", RequestIDFromContext(r.Context()))
			return
		}
		if q.Text == "" {
			WriteError(w, http.StatusBadRequest, "text required", RequestIDFromContext(r.Context()))
			return
		}
		if !clampTopK(w, r, &q) {
			return
		}
		res, err := cfg.Service.Query(r.Context(), q)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	}
}

func handlePostQueryDescribe(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var q memory.Query
		if !decodeStrict(w, r, &q) {
			return
		}
		if !q.Mode.Valid() {
			WriteError(w, http.StatusBadRequest, "invalid mode", RequestIDFromContext(r.Context()))
			return
		}
		if q.Text == "" {
			WriteError(w, http.StatusBadRequest, "text required", RequestIDFromContext(r.Context()))
			return
		}
		if !clampTopK(w, r, &q) {
			return
		}
		res, err := cfg.Service.Describe(r.Context(), q)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	}
}
