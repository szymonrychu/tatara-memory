package ingest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
)

func TestMigrationSQLExists(t *testing.T) {
	sql := ingest.MigrationSQL()
	require.NotEmpty(t, sql)
	require.Contains(t, sql, "CREATE TABLE")
	require.Contains(t, sql, "ingest_jobs")
	require.Contains(t, sql, "ingest_job_items")
	require.Contains(t, sql, "UNIQUE")
}
