package ingest_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/pgmigrate/pgmigratetest"
)

// tatara-memory#107. Nothing was broken here: all three ingest migrations are
// CREATE TABLE IF NOT EXISTS / ADD COLUMN IF NOT EXISTS and replay harmlessly.
// The tracker is what stops the NEXT ingest migration from being the one that
// repeats codegraph/0005, and this test is what makes that guarantee executable
// rather than a comment in another package.
func TestMigrate_ReplayIssuesNoDDL(t *testing.T) {
	db, rec := pgmigratetest.New(t)
	ctx := context.Background()

	require.NoError(t, ingest.Migrate(ctx, db))
	require.Equal(t, ingest.MigrationNames(), rec.Applied())

	rec.Reset()
	require.NoError(t, ingest.Migrate(ctx, db))
	require.Empty(t, rec.MigrationStatements(), "replay must issue no schema DDL")
}

// "Harmless" was never quite true, and this change made it less true. ADD COLUMN
// IF NOT EXISTS takes ACCESS EXCLUSIVE on ingest_job_items BEFORE it discovers
// the column already exists, and CREATE INDEX IF NOT EXISTS takes ShareLock -
// both now under pgmigrate's 5m lock_timeout rather than the request path's 30s.
// Without probes, the single boot that installs the tracker would request those
// locks for a migration set with zero schema effect, which is the exact failure
// shape #107 is about. So ingest baselines too.
func TestMigrate_BaselineStampsWithoutDDL(t *testing.T) {
	db, rec := pgmigratetest.New(t)
	for _, name := range ingest.MigrationNames() {
		rec.SetProbe(name, true)
	}

	require.NoError(t, ingest.Migrate(context.Background(), db))

	require.Empty(t, rec.MigrationStatements(), "an already-migrated database must take no lock on ship day")
	require.Equal(t, ingest.MigrationNames(), rec.Applied())
	require.Equal(t, ingest.MigrationNames(), rec.Probed(), "every migration must carry a baseline probe")
}

func TestMigrate_BaselineStopsAtFirstGap(t *testing.T) {
	db, rec := pgmigratetest.New(t)
	names := ingest.MigrationNames()
	rec.SetProbe(names[0], true)
	rec.SetProbe(names[1], false)
	rec.SetProbe(names[2], true) // satisfied, but baselining is over

	require.NoError(t, ingest.Migrate(context.Background(), db))

	stmts := rec.MigrationStatements()
	require.Len(t, stmts, 2)
	require.Contains(t, stmts[0], "metadata")
	require.Contains(t, stmts[1], "track_id")
	require.Equal(t, names, rec.Applied())
}

// Migration bodies run inside an explicit transaction now, so a statement that
// refuses to run in one fails with SQLSTATE 25001 at boot and leaves the pod
// permanently unready. Nothing else in the unit suite would catch it: the stub
// driver accepts any SQL.
func TestMigrationSQL_IsTransactionSafe(t *testing.T) {
	pgmigratetest.RequireTransactionSafe(t, ingest.MigrationSQL())
}
