package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func handleListEdges(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		es, err := cfg.Service.ListEdges(r.Context())
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"edges": es})
	}
}

func handleCreateEdge(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var e memory.Edge
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
			return
		}
		if e.From == "" || e.To == "" || e.Relation == "" {
			WriteError(w, http.StatusBadRequest, "from_entity, to_entity, relation required", RequestIDFromContext(r.Context()))
			return
		}
		created, err := cfg.Service.CreateEdge(r.Context(), e)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		cfg.Logger.InfoContext(r.Context(), "edge.create",
			"action", "create_edge",
			"request_id", RequestIDFromContext(r.Context()),
			"user", claimSubject(r),
			"resource_id", created.ID,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		WriteJSON(w, http.StatusCreated, created)
	}
}

func handleDeleteEdge(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := chi.URLParam(r, "id")
		if err := cfg.Service.DeleteEdge(r.Context(), id); err != nil {
			mapServiceError(w, r, err)
			return
		}
		cfg.Logger.InfoContext(r.Context(), "edge.delete",
			"action", "delete_edge",
			"request_id", RequestIDFromContext(r.Context()),
			"user", claimSubject(r),
			"resource_id", id,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		w.WriteHeader(http.StatusNoContent)
	}
}
