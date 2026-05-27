package memory

import (
	"context"
	"database/sql"
	_ "embed"
)

//go:embed migrations/0001_tombstones.sql
var migration0001 string

// MigrationSQL returns the DDL for the memory schema (currently the
// tombstone table for soft-deleted memory IDs).
func MigrationSQL() string {
	return migration0001
}

// Migrate applies the memory schema to db, creating tables if they do
// not exist.
func Migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, migration0001)
	return err
}
