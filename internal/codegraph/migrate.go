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

//go:embed migrations/0003_phase0_graphify.sql
var migration0003 string

//go:embed migrations/0004_phase2_semantic.sql
var migration0004 string

//go:embed migrations/0005_audit_fixes.sql
var migration0005 string

// MigrationSQL returns the DDL for the code-graph schema (all migrations concatenated).
func MigrationSQL() string {
	return migration0001 + "\n" + migration0002 + "\n" + migration0003 + "\n" + migration0004 + "\n" + migration0005
}

// Migrate applies the code-graph schema to db, creating tables if they do not exist.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, migration0001); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, migration0002); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, migration0003); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, migration0004); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, migration0005)
	return err
}
