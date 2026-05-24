package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
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
		var e Edge
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
		WriteJSON(w, http.StatusCreated, created)
	}
}

func handleDeleteEdge(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Service.DeleteEdge(r.Context(), chi.URLParam(r, "id")); err != nil {
			mapServiceError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
