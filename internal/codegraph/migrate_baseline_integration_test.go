//go:build integration

package codegraph_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

// pkeyOID returns the OID of code_edges' primary-key constraint. A DROP
// CONSTRAINT + ADD PRIMARY KEY mints a new one, so this is a direct,
// server-side observation of whether 0005 rebuilt the index - the exact harm in
// tatara-memory#107 - rather than an assertion about which statements were sent.
func pkeyOID(t *testing.T, ctx context.Context, db *sql.DB) uint32 {
	t.Helper()
	var oid uint32
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT c.oid FROM pg_constraint c
		 WHERE c.conrelid = to_regclass('code_edges') AND c.contype = 'p'`).Scan(&oid))
	return oid
}

// The replay guard, against a real server. The unit test asserts that no DDL is
// sent; this asserts that nothing moved when it was not.
func TestMigrate_ReplayDoesNotRebuildThePrimaryKey(t *testing.T) {
	ctx := context.Background()
	db := openPG(t)
	require.NoError(t, codegraph.Migrate(ctx, db))
	before := pkeyOID(t, ctx, db)

	require.NoError(t, codegraph.Migrate(ctx, db))

	require.Equal(t, before, pkeyOID(t, ctx, db), "replay rebuilt code_edges_pkey under ACCESS EXCLUSIVE")
}

// B1 end to end. Dropping the tracker leaves exactly the shape of the
// production database at the moment this change is deployed: the full end state
// of all six migrations and no record of any of them. Every baseline probe runs
// for real here, which is the only place their SQL is executed at all - the unit
// suite stubs the answers.
func TestMigrate_BaselinesADatabaseWithNoTracker(t *testing.T) {
	ctx := context.Background()
	db := openPG(t)
	require.NoError(t, codegraph.Migrate(ctx, db))
	before := pkeyOID(t, ctx, db)

	_, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS `+codegraph.TrackerTable())
	require.NoError(t, err)

	require.NoError(t, codegraph.Migrate(ctx, db))

	var applied int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM `+codegraph.TrackerTable()).Scan(&applied))
	require.Equal(t, len(codegraph.MigrationNames()), applied,
		"every migration must be stamped by its baseline probe")
	require.Equal(t, before, pkeyOID(t, ctx, db),
		"the upgrade that ships the tracker must issue no DDL: code_edges_pkey was rebuilt")
}
