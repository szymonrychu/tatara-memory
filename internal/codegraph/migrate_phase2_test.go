//go:build integration

package codegraph_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

func TestMigratePhase2Schema(t *testing.T) {
	_, db, ctx := freshStoreWithDB(t)

	tableExists := func(name string) bool {
		var ok bool
		require.NoError(t, db.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name=$1)`, name).Scan(&ok))
		return ok
	}
	columnExists := func(table, col string) bool {
		var ok bool
		require.NoError(t, db.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name=$1 AND column_name=$2)`, table, col).Scan(&ok))
		return ok
	}

	require.True(t, columnExists("code_edges", "extractor"))
	require.True(t, columnExists("code_entities", "extractor"))
	require.True(t, columnExists("code_hyperedges", "extractor"))
	require.True(t, tableExists("semantic_extractions"))
	require.True(t, tableExists("code_communities"))
	require.True(t, tableExists("repo_analytics_state"))

	_, err := db.ExecContext(ctx, `INSERT INTO code_entities(repo, id, name, type, file_path) VALUES ('m4','m4:e','e','go_func','a.go')`)
	require.NoError(t, err)
	var extractor string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT extractor FROM code_entities WHERE repo='m4' AND id='m4:e'`).Scan(&extractor))
	require.Equal(t, "ast", extractor)

	_ = codegraph.MigrationSQL()
}
