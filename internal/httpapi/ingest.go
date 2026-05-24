package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type bulkRequest struct {
	Items []memory.IngestItem `json:"items"`
}

func handleBulkIngest(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req bulkRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
			return
		}
		if len(req.Items) == 0 {
			WriteError(w, http.StatusBadRequest, "items must not be empty", RequestIDFromContext(r.Context()))
			return
		}
		job, err := cfg.Ingest.Enqueue(r.Context(), req.Items)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusAccepted, job)
	}
}

func handleGetJob(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		job, err := cfg.Ingest.GetJob(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, job)
	}
}
