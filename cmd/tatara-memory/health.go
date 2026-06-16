package main

import (
	"context"
	"fmt"
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
// It returns the first dependency error it encounters, wrapped with the component name.
func readyzFunc(db pinger, lr healther) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := db.PingContext(ctx); err != nil {
			return fmt.Errorf("db: %w", err)
		}
		if err := lr.Health(ctx); err != nil {
			return fmt.Errorf("lightrag: %w", err)
		}
		return nil
	}
}
