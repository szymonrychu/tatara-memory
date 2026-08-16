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
// No baseline probes. All three migrations are CREATE TABLE IF NOT EXISTS /
// ADD COLUMN IF NOT EXISTS, so re-running them against the already-migrated
// production database costs one catalog-only pass on the first boot after the
// tracker lands and nothing thereafter. Nothing here was broken; the tracker is
// what stops the NEXT ingest migration from being a codegraph/0005.
var runner = pgmigrate.Runner{
	Tracker: "ingest_schema_migrations",
	Migrations: []pgmigrate.Migration{
		{Name: "0001_jobs", SQL: migration0001},
		{Name: "0002_job_item_payload", SQL: migration0002},
		{Name: "0003_item_track_id", SQL: migration0003},
	},
}

// MigrationSQL returns the DDL for the ingest schema (all migrations concatenated).
func MigrationSQL() string { return runner.SQL() }

// MigrationNames returns the ordered migration names recorded in the tracker.
func MigrationNames() []string { return runner.Names() }

// TrackerTable returns the name of this package's version-tracking table.
func TrackerTable() string { return runner.Tracker }

// Migrate applies the ingest schema to db via the shared version-tracked
// runner. Already-applied migrations are skipped, so Migrate is safe to call on
// every startup.
func Migrate(ctx context.Context, db *sql.DB) error {
	if err := runner.Run(ctx, db); err != nil {
		return fmt.Errorf("ingest migrate: %w", err)
	}
	return nil
}
