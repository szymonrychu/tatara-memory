package memory

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"

	"github.com/szymonrychu/tatara-memory/internal/pgmigrate"
)

//go:embed migrations/0001_tombstones.sql
var migration0001 string

//go:embed migrations/0002_memory_sources.sql
var migration0002 string

//go:embed migrations/0003_tombstone_backoff.sql
var migration0003 string

// runner is the ordered migration set for this package. The names are recorded
// in memory_schema_migrations and are already present in the production
// database: neither a name nor the tracker table may be renamed, or every
// migration re-runs. No baseline probes - this package has had a version
// tracker since 2026-06-16, so its tracker rows already exist everywhere.
var runner = pgmigrate.Runner{
	Tracker: "memory_schema_migrations",
	Migrations: []pgmigrate.Migration{
		{Name: "0001_tombstones", SQL: migration0001},
		{Name: "0002_memory_sources", SQL: migration0002},
		{Name: "0003_tombstone_backoff", SQL: migration0003},
	},
}

// MigrationSQL returns the DDL for the memory schema (tombstones plus the
// repo/file -> track_id source index used by per-file reconcile).
func MigrationSQL() string { return runner.SQL() }

// MigrationNames returns the ordered migration names recorded in the tracker.
func MigrationNames() []string { return runner.Names() }

// TrackerTable returns the name of this package's version-tracking table.
func TrackerTable() string { return runner.Tracker }

// Migrate applies the memory schema to db via the shared version-tracked
// runner. Already-applied migrations are skipped, so Migrate is safe to call on
// every startup; see internal/pgmigrate for the tracker and baseline rules.
func Migrate(ctx context.Context, db *sql.DB) error {
	if err := runner.Run(ctx, db); err != nil {
		return fmt.Errorf("memory migrate: %w", err)
	}
	return nil
}
