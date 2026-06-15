package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// maxSmallBody is the body-size cap for small (non-bulk) POST/PATCH endpoints.
// 1 MiB is generous for any structured query payload; bulk endpoints use maxBulkBody.
const maxSmallBody = 1 << 20 // 1 MiB

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
		res, err := cfg.Service.Describe(r.Context(), q)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	}
}
