package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/szymonrychu/tatara-memory/internal/auth"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func handlePostMemory(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var m memory.Memory
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
			return
		}
		created, err := cfg.Service.CreateMemory(r.Context(), m)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		cfg.Logger.InfoContext(r.Context(), "memory.create",
			"action", "create_memory",
			"request_id", RequestIDFromContext(r.Context()),
			"user", claimSubject(r),
			"resource_id", created.ID,
			"duration_ms", time.Since(start).Milliseconds(),
		)
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
		start := time.Now()
		id := chi.URLParam(r, "id")
		if err := cfg.Service.DeleteMemory(r.Context(), id); err != nil {
			mapServiceError(w, r, err)
			return
		}
		cfg.Logger.InfoContext(r.Context(), "memory.delete",
			"action", "delete_memory",
			"request_id", RequestIDFromContext(r.Context()),
			"user", claimSubject(r),
			"resource_id", id,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		w.WriteHeader(http.StatusNoContent)
	}
}

// claimSubject extracts the authenticated subject from the request context,
// returning an empty string when no claims are present (unauthenticated paths).
func claimSubject(r *http.Request) string {
	if c, ok := auth.ClaimsFromContext(r.Context()); ok {
		return c.Subject
	}
	return ""
}
