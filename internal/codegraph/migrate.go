package codegraph

import (
	"context"
	"database/sql"
	_ "embed"
)

//go:embed migrations/0001_codegraph.sql
var migration0001 string

// MigrationSQL returns the DDL for the code-graph schema.
func MigrationSQL() string {
	return migration0001
}

// Migrate applies the code-graph schema to db, creating tables if they do not exist.
func Migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, migration0001)
	return err
}
