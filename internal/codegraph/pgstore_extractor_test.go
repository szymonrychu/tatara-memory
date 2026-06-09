//go:build integration

package codegraph_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

func TestReconcile_ExtractorScopedDeletesPreserveOtherOrigin(t *testing.T) {
	s, db, ctx := freshStoreWithDB(t)

	// AST push for a.go
	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:      "ex",
		Extractor: codegraph.ExtractorAST,
		Files:     []string{"a.go"},
		Entities:  []codegraph.Entity{ent("ex:a", "go_func", "a.go"), ent("ex:b", "go_func", "a.go")},
		Edges:     []codegraph.Edge{{From: "ex:a", To: "ex:b", Relation: "calls", SrcFile: "a.go"}},
	})
	require.NoError(t, err)

	// semantic push for the SAME file a.go
	_, err = s.Reconcile(ctx, codegraph.GraphPush{
		Repo:      "ex",
		Extractor: codegraph.ExtractorSemantic,
		Files:     []string{"a.go"},
		Entities:  []codegraph.Entity{{ID: "concept:ex:auth", Name: "auth", Type: codegraph.EntityConcept, FilePath: "a.go"}},
		Edges:     []codegraph.Edge{{From: "ex:a", To: "concept:ex:auth", Relation: codegraph.RelConceptuallyRelated, SrcFile: "a.go"}},
		FileSHAs:  map[string]string{"a.go": "sha-1"},
	})
	require.NoError(t, err)

	count := func(q string) int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx, q).Scan(&n))
		return n
	}
	// Both origins coexist.
	require.Equal(t, 2, count(`SELECT count(*) FROM code_entities WHERE repo='ex' AND extractor='ast'`))
	require.Equal(t, 1, count(`SELECT count(*) FROM code_entities WHERE repo='ex' AND extractor='semantic'`))
	require.Equal(t, 1, count(`SELECT count(*) FROM code_edges WHERE repo='ex' AND extractor='ast'`))
	require.Equal(t, 1, count(`SELECT count(*) FROM code_edges WHERE repo='ex' AND extractor='semantic'`))

	// Re-ingest AST for a.go: semantic rows survive.
	_, err = s.Reconcile(ctx, codegraph.GraphPush{
		Repo:      "ex",
		Extractor: codegraph.ExtractorAST,
		Files:     []string{"a.go"},
		Entities:  []codegraph.Entity{ent("ex:a", "go_func", "a.go")},
	})
	require.NoError(t, err)
	require.Equal(t, 1, count(`SELECT count(*) FROM code_entities WHERE repo='ex' AND extractor='ast'`))
	require.Equal(t, 1, count(`SELECT count(*) FROM code_entities WHERE repo='ex' AND extractor='semantic'`), "semantic entity must survive AST re-ingest")
	require.Equal(t, 1, count(`SELECT count(*) FROM code_edges WHERE repo='ex' AND extractor='semantic'`), "semantic edge must survive AST re-ingest")

	// semantic_extractions cache row written.
	var sha string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT content_sha FROM semantic_extractions WHERE repo='ex' AND file_path='a.go'`).Scan(&sha))
	require.Equal(t, "sha-1", sha)

	// repo_analytics_state marked dirty on reconcile.
	var dirty bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT dirty FROM repo_analytics_state WHERE repo='ex'`).Scan(&dirty))
	require.True(t, dirty)
}

func TestReconcile_DefaultExtractorIsAST(t *testing.T) {
	s, db, ctx := freshStoreWithDB(t)
	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:     "def",
		Files:    []string{"a.go"},
		Entities: []codegraph.Entity{ent("def:a", "go_func", "a.go")},
	})
	require.NoError(t, err)
	var extractor string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT extractor FROM code_entities WHERE repo='def' AND id='def:a'`).Scan(&extractor))
	require.Equal(t, "ast", extractor)
}
