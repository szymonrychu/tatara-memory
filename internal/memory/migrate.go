package memory

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
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

// migrations is the ordered set of named migrations for this package.
// Each entry is (name, sql). Names match the embedded file name so that
// the schema_migrations tracking table records a human-readable applied set.
var migrations = []struct {
	name string
	sql  string
}{
	{"0001_tombstones", migration0001},
	{"0002_memory_sources", migration0002},
}

const createSchemaMigrations = `
CREATE TABLE IF NOT EXISTS memory_schema_migrations (
    name       TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// Migrate applies the memory schema to db using a version-tracked migration
// runner. A memory_schema_migrations table records which migrations have been
// applied; each unapplied migration runs in its own transaction so a failure
// leaves the table in a consistent state with a clear high-water mark.
// Already-applied migrations are skipped, making Migrate safe to call on every
// startup without re-running idempotent or future non-idempotent migrations.
func Migrate(ctx context.Context, db *sql.DB) error {
	// Bootstrap the tracker table first (idempotent CREATE IF NOT EXISTS).
	if _, err := db.ExecContext(ctx, createSchemaMigrations); err != nil {
		return fmt.Errorf("memory migrate: create tracker: %w", err)
	}

	for _, m := range migrations {
		applied, err := migrationApplied(ctx, db, m.name)
		if err != nil {
			return fmt.Errorf("memory migrate: check %s: %w", m.name, err)
		}
		if applied {
			continue
		}
		if err := applyMigration(ctx, db, m.name, m.sql); err != nil {
			return fmt.Errorf("memory migrate: apply %s: %w", m.name, err)
		}
	}
	return nil
}

func migrationApplied(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM memory_schema_migrations WHERE name = $1)`, name).
		Scan(&exists)
	return exists, err
}

func applyMigration(ctx context.Context, db *sql.DB, name, sqlStr string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, sqlStr); err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memory_schema_migrations (name) VALUES ($1)`, name); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	return tx.Commit()
}
