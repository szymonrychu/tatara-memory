package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func handlePostQuery(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var q memory.Query
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
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
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
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
