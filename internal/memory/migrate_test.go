package memory_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestMigrationSQLExists(t *testing.T) {
	sql := memory.MigrationSQL()
	require.NotEmpty(t, sql)
	require.Contains(t, sql, "CREATE TABLE")
	require.Contains(t, sql, "deleted_memories")
	require.Contains(t, sql, "PRIMARY KEY")
}

func TestMigrationSQLMemorySources(t *testing.T) {
	sql := memory.MigrationSQL()
	require.Contains(t, sql, "memory_sources")
	require.Contains(t, sql, "track_id")
	require.Contains(t, sql, "memory_sources_repo_file")
}

// The tombstone-reaper backoff migration must be additive (ADD COLUMN IF NOT
// EXISTS) so it is safe to apply against an existing deleted_memories table -
// migrations must remain strictly idempotent per applyMigration's contract
// (see migrate.go doc comment on the ON CONFLICT DO NOTHING tracker insert).
func TestMigrationSQLTombstoneBackoff(t *testing.T) {
	sql := memory.MigrationSQL()
	require.Contains(t, sql, "force_reap_attempts")
	require.Contains(t, sql, "next_force_check_at")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS",
		"tombstone backoff migration must use ADD COLUMN IF NOT EXISTS for idempotency")
}

// Finding 6: Migrate must use a version-tracking table so non-idempotent future
// migrations can be applied safely.
func TestMigrate_VersionTracking(t *testing.T) {
	// Verify the ordered migration list is non-empty and uniquely named.
	names := memory.MigrationNames()
	require.NotEmpty(t, names, "migrations slice must not be empty (finding 6)")
	seen := map[string]struct{}{}
	for _, n := range names {
		require.NotEmpty(t, n, "each migration must have a non-empty name")
		_, dup := seen[n]
		require.False(t, dup, "migration names must be unique, duplicate: %q", n)
		seen[n] = struct{}{}
	}
	// Names must be in lexicographic/chronological order (0001 < 0002 ...).
	for i := 1; i < len(names); i++ {
		require.True(t, names[i-1] < names[i],
			"migrations must be in ascending order: %q >= %q", names[i-1], names[i])
	}

	// Verify the tracker table DDL references the expected table name.
	trackerSQL := memory.CreateSchemaMigrationsSQL()
	require.Contains(t, strings.ToLower(trackerSQL), "memory_schema_migrations",
		"tracker table must be named memory_schema_migrations (finding 6)")
	require.Contains(t, strings.ToLower(trackerSQL), "name",
		"tracker table must record migration name (finding 6)")
}
