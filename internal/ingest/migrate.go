package ingest

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"

	"github.com/szymonrychu/tatara-memory/internal/pgmigrate"
)

//go:embed migrations/0001_jobs.sql
var migration0001 string

//go:embed migrations/0002_job_item_payload.sql
var migration0002 string

//go:embed migrations/0003_item_track_id.sql
var migration0003 string

// runner is the ordered migration set for this package.
//
// Nothing here was broken by tatara-memory#107: all three migrations are
// CREATE TABLE IF NOT EXISTS / ADD COLUMN IF NOT EXISTS and replay without
// changing the schema. The tracker is what stops the NEXT ingest migration from
// being a codegraph/0005.
//
// They still carry baseline probes, because "replays harmlessly" is about the
// schema, not about locks. ADD COLUMN IF NOT EXISTS takes ACCESS EXCLUSIVE on
// ingest_job_items BEFORE it discovers the column is already there, and
// CREATE INDEX IF NOT EXISTS takes ShareLock - and pgmigrate now waits 5m for a
// lock rather than the request path's 30s. Without probes, the single boot that
// installs the tracker would request those locks for a migration set with zero
// schema effect, which is the exact shape #107 is about.
var runner = pgmigrate.Runner{
	Tracker: "ingest_schema_migrations",
	Migrations: []pgmigrate.Migration{
		{
			Name: "0001_jobs",
			SQL:  migration0001,
			AlreadyApplied: pgmigrate.Probe("0001_jobs", `
SELECT to_regclass('ingest_jobs') IS NOT NULL
   AND to_regclass('ingest_job_items') IS NOT NULL`),
		},
		{
			Name: "0002_job_item_payload",
			SQL:  migration0002,
			AlreadyApplied: pgmigrate.Probe("0002_job_item_payload", `
SELECT EXISTS (SELECT 1 FROM pg_attribute
                WHERE attrelid = to_regclass('ingest_job_items')
                  AND attname = 'text' AND NOT attisdropped)
   AND EXISTS (SELECT 1 FROM pg_attribute
                WHERE attrelid = to_regclass('ingest_job_items')
                  AND attname = 'metadata' AND NOT attisdropped)`),
		},
		{
			Name: "0003_item_track_id",
			SQL:  migration0003,
			AlreadyApplied: pgmigrate.Probe("0003_item_track_id", `
SELECT EXISTS (SELECT 1 FROM pg_attribute
                WHERE attrelid = to_regclass('ingest_job_items')
                  AND attname = 'track_id' AND NOT attisdropped)`),
		},
	},
}

// MigrationSQL returns the DDL for the ingest schema (all migrations concatenated).
func MigrationSQL() string { return runner.SQL() }

// MigrationNames returns the ordered migration names recorded in the tracker.
func MigrationNames() []string { return runner.Names() }

// Migrate applies the ingest schema to db via the shared version-tracked
// runner. Already-applied migrations are skipped, so Migrate is safe to call on
// every startup.
func Migrate(ctx context.Context, db *sql.DB) error {
	if err := runner.Run(ctx, db); err != nil {
		return fmt.Errorf("ingest migrate: %w", err)
	}
	return nil
}
