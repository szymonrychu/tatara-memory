package ingest

import (
	"context"
	"database/sql"
	_ "embed"
)

//go:embed migrations/0001_jobs.sql
var migration0001 string

//go:embed migrations/0002_job_item_payload.sql
var migration0002 string

//go:embed migrations/0003_item_track_id.sql
var migration0003 string

// MigrationSQL returns the DDL for the ingest schema (all migrations concatenated).
func MigrationSQL() string {
	return migration0001 + "\n" + migration0002 + "\n" + migration0003
}

// Migrate applies the ingest schema to db, creating tables if they do not exist.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, migration0001); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, migration0002); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, migration0003)
	return err
}
