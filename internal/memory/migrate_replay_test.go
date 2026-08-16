package memory_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
	"github.com/szymonrychu/tatara-memory/internal/pgmigrate/pgmigratetest"
)

// tatara-memory#107. memory already had a version tracker; this pins the
// behaviour through the move to the shared runner, and in particular that the
// tracker table and the migration names are unchanged - a rename of either
// re-runs every migration against the production database.
func TestMigrate_ReplayIssuesNoDDL(t *testing.T) {
	db, rec := pgmigratetest.New(t)
	ctx := context.Background()

	require.NoError(t, memory.Migrate(ctx, db))
	require.Equal(t, memory.MigrationNames(), rec.Applied())

	rec.Reset()
	require.NoError(t, memory.Migrate(ctx, db))
	require.Empty(t, rec.MigrationStatements(), "replay must issue no schema DDL")
}
