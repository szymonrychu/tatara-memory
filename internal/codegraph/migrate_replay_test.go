package codegraph_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
	"github.com/szymonrychu/tatara-memory/internal/pgmigrate/pgmigratetest"
)

// tatara-memory#107. Before the tracker, Migrate issued all six migrations on
// every process start, and 0005 is not re-runnable: it DROPs and re-ADDs
// code_edges_pkey, an ACCESS EXCLUSIVE index rebuild on the largest table in
// the graph, on a pooled connection carrying the 30s request-path lock_timeout,
// while the outgoing pod of a rolling upgrade is still serving.
func TestMigrate_ReplayIssuesNoDDL(t *testing.T) {
	db, rec := pgmigratetest.New(t)
	ctx := context.Background()

	require.NoError(t, codegraph.Migrate(ctx, db))
	require.Equal(t, codegraph.MigrationNames(), rec.Applied(), "first run must apply every migration")

	rec.Reset()
	require.NoError(t, codegraph.Migrate(ctx, db))
	require.Empty(t, rec.MigrationStatements(),
		"replay must issue no schema DDL; 0005 rebuilds code_edges_pkey under ACCESS EXCLUSIVE")
}

// B1. A database migrated before the tracker existed has no tracker rows but
// already has the end state of all six migrations. It must be stamped, not
// re-migrated: the rollout that ships this fix must take no DDL lock at all.
func TestMigrate_BaselineStampsWithoutDDL(t *testing.T) {
	db, rec := pgmigratetest.New(t)
	for _, name := range codegraph.MigrationNames() {
		rec.SetProbe(name, true)
	}

	require.NoError(t, codegraph.Migrate(context.Background(), db))

	require.Empty(t, rec.MigrationStatements(), "a fully migrated database must be baselined, not re-migrated")
	require.Equal(t, codegraph.MigrationNames(), rec.Applied())
	require.Equal(t, codegraph.MigrationNames(), rec.Probed(),
		"every migration must carry a baseline probe; one without it executes against the production database that already has its effect")
}

// Pre-mortem 1. A database that stopped at 0004 must NOT be stamped as fully
// migrated: 0005 is the migration that puts extractor in the code_edges primary
// key, and skipping it leaks an edge per (from,to,relation) collision forever.
func TestMigrate_BaselineStopsAtFirstGap(t *testing.T) {
	db, rec := pgmigratetest.New(t)
	names := codegraph.MigrationNames()
	for _, name := range names[:4] {
		rec.SetProbe(name, true)
	}
	rec.SetProbe(names[4], false)
	rec.SetProbe(names[5], false)

	require.NoError(t, codegraph.Migrate(context.Background(), db))

	stmts := rec.MigrationStatements()
	require.Len(t, stmts, 2, "0005 and 0006 must execute against a database stuck at 0004")
	require.Contains(t, stmts[0], "ADD PRIMARY KEY (repo, from_id, to_id, relation, extractor)")
	require.Contains(t, stmts[1], "ALTER COLUMN betweenness TYPE double precision")
	require.Equal(t, names, rec.Applied())
}

// Migration bodies run inside an explicit transaction now, so a statement that
// refuses to run in one fails with SQLSTATE 25001 at boot and leaves the pod
// permanently unready. Nothing else in the unit suite would catch it: the stub
// driver accepts any SQL. codegraph is the package most likely to want
// CREATE INDEX CONCURRENTLY, since its growth pattern is another code_edges index.
func TestMigrationSQL_IsTransactionSafe(t *testing.T) {
	pgmigratetest.RequireTransactionSafe(t, codegraph.MigrationSQL())
}
