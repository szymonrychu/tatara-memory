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
//
// startup is checked first and is what makes tatara-memory#102's fix visible to
// Kubernetes: the database wait, the schema migration and the ingest resume now
// run in the background, so between process start and their completion this pod
// is up but must not take traffic. A nil startup means "no deferred startup",
// which is what the unit tests and any future caller without one get.
func readyzFunc(startup func() error, db pinger, lr healther) func(context.Context) error {
	return func(ctx context.Context) error {
		if startup != nil {
			if err := startup(); err != nil {
				return fmt.Errorf("startup: %w", err)
			}
		}
		if err := db.PingContext(ctx); err != nil {
			return fmt.Errorf("db: %w", err)
		}
		if err := lr.Health(ctx); err != nil {
			return fmt.Errorf("lightrag: %w", err)
		}
		return nil
	}
}
