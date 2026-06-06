//go:build integration

package codegraph_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

func openPG(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TATARA_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TATARA_TEST_PG_DSN not set; skipping integration test")
	}
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func freshStore(t *testing.T) (*codegraph.PGStore, context.Context) {
	t.Helper()
	ctx := context.Background()
	db := openPG(t)
	require.NoError(t, codegraph.Migrate(ctx, db))
	_, err := db.ExecContext(ctx, `DELETE FROM cross_repo_symbols; DELETE FROM code_edges; DELETE FROM code_entities;`)
	require.NoError(t, err)
	return codegraph.NewPGStore(db), ctx
}

func TestMigrateCrossRepoSymbolsTable(t *testing.T) {
	ctx := context.Background()
	db := openPG(t)
	require.NoError(t, codegraph.Migrate(ctx, db))

	var exists bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM information_schema.tables
			WHERE table_name = 'cross_repo_symbols'
		)`).Scan(&exists)
	require.NoError(t, err)
	require.True(t, exists, "cross_repo_symbols table must exist after Migrate")
}

func ent(id, typ, file string) codegraph.Entity {
	return codegraph.Entity{ID: id, Name: id, Type: typ, FilePath: file, Properties: map[string]string{"language": "go"}}
}

func TestReconcileInsertsAndReplacesPerFile(t *testing.T) {
	s, ctx := freshStore(t)

	res, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "r",
		Files: []string{"a.go", "b.go"},
		Entities: []codegraph.Entity{
			ent("go:func:r/a.A", "go_func", "a.go"),
			ent("go:func:r/b.B", "go_func", "b.go"),
		},
		Edges: []codegraph.Edge{
			{From: "go:func:r/a.A", To: "go:func:r/b.B", Relation: "calls", SrcFile: "a.go"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 2, res.EntitiesUpserted)
	require.Equal(t, 1, res.EdgesUpserted)

	n, err := s.CountEntities(ctx, "r")
	require.NoError(t, err)
	require.Equal(t, 2, n)

	_, err = s.Reconcile(ctx, codegraph.GraphPush{
		Repo:     "r",
		Files:    []string{"a.go"},
		Entities: []codegraph.Entity{ent("go:func:r/a.A", "go_func", "a.go")},
		Edges:    nil,
	})
	require.NoError(t, err)

	n, err = s.CountEntities(ctx, "r")
	require.NoError(t, err)
	require.Equal(t, 2, n)

	callees, err := s.Neighbors(ctx, "r", "go:func:r/a.A", []string{"calls"}, "out", 3)
	require.NoError(t, err)
	require.Empty(t, callees)
}
