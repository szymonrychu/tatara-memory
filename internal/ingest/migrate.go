package ingest

import (
	"context"
	"database/sql"
	_ "embed"
)

//go:embed migrations/0001_jobs.sql
var migration0001 string

// MigrationSQL returns the DDL for the ingest schema.
func MigrationSQL() string {
	return migration0001
}

// Migrate applies the ingest schema to db, creating tables if they do not exist.
func Migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, migration0001)
	return err
}
