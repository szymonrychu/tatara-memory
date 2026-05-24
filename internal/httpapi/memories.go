package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func handlePostMemory(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var m Memory
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
			return
		}
		created, err := cfg.Service.CreateMemory(r.Context(), m)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusCreated, created)
	}
}

func handleGetMemory(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		m, err := cfg.Service.GetMemory(r.Context(), id)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, m)
	}
}

func handleDeleteMemory(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := cfg.Service.DeleteMemory(r.Context(), id); err != nil {
			mapServiceError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
