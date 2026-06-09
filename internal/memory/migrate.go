package memory

import (
	"context"
	"database/sql"
	_ "embed"
)

//go:embed migrations/0001_tombstones.sql
var migration0001 string

//go:embed migrations/0002_memory_sources.sql
var migration0002 string

// MigrationSQL returns the DDL for the memory schema (tombstones plus the
// repo/file -> track_id source index used by per-file reconcile).
func MigrationSQL() string {
	return migration0001 + "\n" + migration0002
}

// Migrate applies the memory schema to db, creating tables if they do
// not exist.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, migration0001); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, migration0002)
	return err
}
