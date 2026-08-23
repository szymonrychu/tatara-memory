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

func freshStoreWithDB(t *testing.T) (*codegraph.PGStore, *sql.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db := openPG(t)
	require.NoError(t, codegraph.Migrate(ctx, db))
	_, err := db.ExecContext(ctx, `DELETE FROM code_hyperedge_members; DELETE FROM code_hyperedges; DELETE FROM cross_repo_symbols; DELETE FROM code_edges; DELETE FROM code_entities; DELETE FROM semantic_extractions; DELETE FROM code_communities; DELETE FROM repo_analytics_state;`)
	require.NoError(t, err)
	return codegraph.NewPGStore(db), db, ctx
}

func freshStore(t *testing.T) (*codegraph.PGStore, context.Context) {
	t.Helper()
	s, _, ctx := freshStoreWithDB(t)
	return s, ctx
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

func TestReconcileSymbolsPerFileReplacement(t *testing.T) {
	s, db, ctx := freshStoreWithDB(t)

	// Push symbols for files a.go and b.go
	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "repo-a",
		Files: []string{"a.go", "b.go"},
		Symbols: []codegraph.SymbolRow{
			{Symbol: "Foo", Lang: "go", Kind: "func", Role: codegraph.RoleProvides, EntityID: "e1", SrcFile: "a.go"},
			{Symbol: "Bar", Lang: "go", Kind: "func", Role: codegraph.RoleRequires, EntityID: "e2", SrcFile: "b.go"},
		},
	})
	require.NoError(t, err)

	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM cross_repo_symbols WHERE repo='repo-a'`).Scan(&count))
	require.Equal(t, 2, count)

	// Re-push a.go with different symbols; b.go's symbols should remain
	_, err = s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "repo-a",
		Files: []string{"a.go"},
		Symbols: []codegraph.SymbolRow{
			{Symbol: "Baz", Lang: "go", Kind: "func", Role: codegraph.RoleProvides, EntityID: "e3", SrcFile: "a.go"},
		},
	})
	require.NoError(t, err)

	// b.go's Bar symbol still present
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM cross_repo_symbols WHERE repo='repo-a' AND src_file='b.go'`).Scan(&count))
	require.Equal(t, 1, count)

	// a.go's Foo gone, Baz present
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM cross_repo_symbols WHERE repo='repo-a' AND symbol='Foo'`).Scan(&count))
	require.Equal(t, 0, count)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM cross_repo_symbols WHERE repo='repo-a' AND symbol='Baz'`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestCrossRepoJoinQuery(t *testing.T) {
	s, _, ctx := freshStoreWithDB(t)

	// repo-a provides "Foo"; repo-b requires "Foo"
	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "repo-a",
		Files: []string{"a.go"},
		Symbols: []codegraph.SymbolRow{
			{Symbol: "Foo", Lang: "go", Kind: "func", Role: codegraph.RoleProvides, EntityID: "ea1", SrcFile: "a.go"},
		},
	})
	require.NoError(t, err)

	_, err = s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "repo-b",
		Files: []string{"b.go"},
		Symbols: []codegraph.SymbolRow{
			{Symbol: "Foo", Lang: "go", Kind: "func", Role: codegraph.RoleRequires, EntityID: "eb1", SrcFile: "b.go"},
		},
	})
	require.NoError(t, err)

	// CrossRepo for repo-a/ea1: should find repo-b as consumer
	linksA, err := s.CrossRepo(ctx, "repo-a", "ea1", 100)
	require.NoError(t, err)
	require.Len(t, linksA.Consumers, 1)
	require.Equal(t, "repo-b", linksA.Consumers[0].Repo)
	require.Equal(t, "eb1", linksA.Consumers[0].EntityID)
	require.Empty(t, linksA.Providers)

	// CrossRepo for repo-b/eb1: should find repo-a as provider
	linksB, err := s.CrossRepo(ctx, "repo-b", "eb1", 100)
	require.NoError(t, err)
	require.Len(t, linksB.Providers, 1)
	require.Equal(t, "repo-a", linksB.Providers[0].Repo)
	require.Equal(t, "ea1", linksB.Providers[0].EntityID)
	require.Empty(t, linksB.Consumers)

	// Self-repo excluded: repo-a should not appear as its own consumer/provider
	for _, c := range linksA.Consumers {
		require.NotEqual(t, "repo-a", c.Repo)
	}
	for _, p := range linksB.Providers {
		require.NotEqual(t, "repo-b", p.Repo)
	}
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

	callees, err := s.Neighbors(ctx, "r", "go:func:r/a.A", []string{"calls"}, "out", 3, 1000, codegraph.ConfidenceFilter{})
	require.NoError(t, err)
	require.Empty(t, callees)
}

func TestReconcileWritesConfidenceColumns(t *testing.T) {
	s, db, ctx := freshStoreWithDB(t)

	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "rc",
		Files: []string{"a.go"},
		Entities: []codegraph.Entity{
			ent("go:func:rc/a.A", "go_func", "a.go"),
			ent("go:func:rc/a.B", "go_func", "a.go"),
		},
		Edges: []codegraph.Edge{
			// explicit confidence
			{From: "go:func:rc/a.A", To: "go:func:rc/a.B", Relation: "calls", SrcFile: "a.go",
				ConfidenceScore: 0.98, ConfidenceTier: codegraph.TierInferred},
			// omitted confidence -> server defaults
			{From: "go:func:rc/a.B", To: "go:func:rc/a.A", Relation: "references", SrcFile: "a.go"},
		},
	})
	require.NoError(t, err)

	var score float64
	var tier string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT confidence_score, confidence_tier FROM code_edges WHERE repo='rc' AND relation='calls'`).Scan(&score, &tier))
	require.InDelta(t, 0.98, score, 1e-6)
	require.Equal(t, "INFERRED", tier)

	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT confidence_score, confidence_tier FROM code_edges WHERE repo='rc' AND relation='references'`).Scan(&score, &tier))
	require.InDelta(t, 1.0, score, 1e-6)
	require.Equal(t, "EXTRACTED", tier)
}

func TestReconcileWritesEntityProvenance(t *testing.T) {
	s, db, ctx := freshStoreWithDB(t)

	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "rp",
		Files: []string{"README.md"},
		Entities: []codegraph.Entity{
			{ID: "doc:section:README.md#intro", Name: "intro", Type: codegraph.EntityDocSection, FilePath: "README.md",
				LineStart: 1, LineEnd: 9, SourceURL: "https://example/x", Author: "me", CapturedAt: "2026-06-09T00:00:00Z"},
		},
	})
	require.NoError(t, err)

	var ls, le int
	var url, author string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT line_start, line_end, source_url, author FROM code_entities WHERE repo='rp' AND id='doc:section:README.md#intro'`).
		Scan(&ls, &le, &url, &author))
	require.Equal(t, 1, ls)
	require.Equal(t, 9, le)
	require.Equal(t, "https://example/x", url)
	require.Equal(t, "me", author)
}

func TestReconcilePurgesAndInsertsHyperedgesPerFile(t *testing.T) {
	s, db, ctx := freshStoreWithDB(t)

	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "rh",
		Files: []string{"a.go", "b.go"},
		Entities: []codegraph.Entity{
			ent("go:func:rh/a.A", "go_func", "a.go"),
			ent("go:func:rh/a.B", "go_func", "a.go"),
			ent("go:func:rh/a.C", "go_func", "a.go"),
			ent("go:func:rh/b.D", "go_func", "b.go"),
		},
		Hyperedges: []codegraph.Hyperedge{
			{ID: "rh:h1", Label: "trio", Relation: "form", ConfidenceScore: 1.0, SrcFile: "a.go",
				Members: []string{"go:func:rh/a.A", "go:func:rh/a.B", "go:func:rh/a.C"}},
		},
	})
	require.NoError(t, err)

	var hcount, mcount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM code_hyperedges WHERE repo='rh'`).Scan(&hcount))
	require.Equal(t, 1, hcount)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM code_hyperedge_members WHERE repo='rh' AND hyperedge_id='rh:h1'`).Scan(&mcount))
	require.Equal(t, 3, mcount)

	// Re-push a.go with no hyperedges: the a.go-owned hyperedge and its members must be purged.
	_, err = s.Reconcile(ctx, codegraph.GraphPush{
		Repo:     "rh",
		Files:    []string{"a.go"},
		Entities: []codegraph.Entity{ent("go:func:rh/a.A", "go_func", "a.go")},
	})
	require.NoError(t, err)

	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM code_hyperedges WHERE repo='rh'`).Scan(&hcount))
	require.Equal(t, 0, hcount)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM code_hyperedge_members WHERE repo='rh'`).Scan(&mcount))
	require.Equal(t, 0, mcount)
}
