package codegraph

import (
	"context"
	"database/sql"
	_ "embed"
)

//go:embed migrations/0001_codegraph.sql
var migration0001 string

//go:embed migrations/0002_cross_repo_symbols.sql
var migration0002 string

// MigrationSQL returns the DDL for the code-graph schema (all migrations concatenated).
func MigrationSQL() string {
	return migration0001 + "\n" + migration0002
}

// Migrate applies the code-graph schema to db, creating tables if they do not exist.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, migration0001); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, migration0002)
	return err
}
