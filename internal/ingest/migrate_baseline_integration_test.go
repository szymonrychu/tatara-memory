//go:build integration

package ingest_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
)

// The only place the ingest baseline probes are executed against a real server.
// The unit suite stubs their answers, so a syntax error there would surface as a
// permanently unready pod and nothing before this point would have caught it.
func TestMigrate_BaselinesADatabaseWithNoTracker(t *testing.T) {
	ctx := context.Background()
	db := openPG(t)
	defer db.Close()
	require.NoError(t, ingest.Migrate(ctx, db))

	_, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS ingest_schema_migrations`)
	require.NoError(t, err)

	require.NoError(t, ingest.Migrate(ctx, db))

	var applied int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM ingest_schema_migrations`).Scan(&applied))
	require.Equal(t, len(ingest.MigrationNames()), applied,
		"every migration must be stamped by its baseline probe, taking no lock on ingest_job_items")
}
