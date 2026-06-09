package memory_test

import (
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
