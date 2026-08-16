package ingest_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/pgmigrate/pgmigratetest"
)

// tatara-memory#107. Nothing is currently broken here: all three ingest
// migrations are CREATE TABLE IF NOT EXISTS / ADD COLUMN IF NOT EXISTS and
// replay harmlessly. The tracker is what stops the NEXT ingest migration from
// being the one that repeats codegraph/0005, and this test is what makes that
// guarantee executable rather than a comment in another package.
func TestMigrate_ReplayIssuesNoDDL(t *testing.T) {
	db, rec := pgmigratetest.New(t)
	ctx := context.Background()

	require.NoError(t, ingest.Migrate(ctx, db))
	require.Equal(t, ingest.MigrationNames(), rec.Applied())

	rec.Reset()
	require.NoError(t, ingest.Migrate(ctx, db))
	require.Empty(t, rec.MigrationStatements(), "replay must issue no schema DDL")
}
