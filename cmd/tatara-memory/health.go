package main

import (
	"context"
)

// pinger is implemented by *sql.DB and any test double that can check a DB connection.
type pinger interface {
	PingContext(ctx context.Context) error
}

// healther is implemented by lightrag.Client and any test double that can check the LightRAG service.
type healther interface {
	Health(ctx context.Context) error
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
