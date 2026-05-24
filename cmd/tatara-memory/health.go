package main

import (
	"context"
	"encoding/json"
	"net/http"
)

// pinger is implemented by *sql.DB and any test double that can check a DB connection.
type pinger interface {
	PingContext(ctx context.Context) error
}

// healther is implemented by lightrag.Client and any test double that can check the LightRAG service.
type healther interface {
	Health(ctx context.Context) error
}

// healthzHandler returns an http.Handler that always responds 200 "ok".
// Used as a liveness probe: if the process is up, the probe passes.
func healthzHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// readyzHandler returns an http.Handler that pings db and lr.
// Responds 200 if both succeed, 503 with a JSON body listing failures otherwise.
func readyzHandler(db pinger, lr healther) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := readyzFunc(db, lr)(r.Context()); err != nil {
			result := readyzCheck(r.Context(), db, lr)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(result)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"db": "ok", "lightrag": "ok"})
	})
}

// readyzFunc returns a ReadyCheck function suitable for httpapi.Config.ReadyCheck.
func readyzFunc(db pinger, lr healther) func(context.Context) error {
	return func(ctx context.Context) error {
		result := readyzCheck(ctx, db, lr)
		for _, v := range result {
			if v != "ok" {
				return errNotReady
			}
		}
		return nil
	}
}

type notReadyError struct{}

func (notReadyError) Error() string { return "not ready" }

var errNotReady = notReadyError{}

func readyzCheck(ctx context.Context, db pinger, lr healther) map[string]string {
	result := map[string]string{"db": "ok", "lightrag": "ok"}
	if err := db.PingContext(ctx); err != nil {
		result["db"] = err.Error()
	}
	if err := lr.Health(ctx); err != nil {
		result["lightrag"] = err.Error()
	}
	return result
}
