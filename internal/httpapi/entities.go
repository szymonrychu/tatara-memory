package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func handleGetEntity(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		e, err := cfg.Service.GetEntity(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, e)
	}
}

func handleSearchEntities(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			WriteError(w, http.StatusBadRequest, "missing query parameter q", RequestIDFromContext(r.Context()))
			return
		}
		es, err := cfg.Service.SearchEntities(r.Context(), q)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"entities": es})
	}
}

func handlePatchEntity(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var patch Entity
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
			return
		}
		e, err := cfg.Service.PatchEntity(r.Context(), chi.URLParam(r, "id"), patch)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, e)
	}
}
