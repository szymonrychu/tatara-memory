//go:build integration

package codegraph_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

// entWithLines creates an Entity with line numbers for EntityExplain tests.
func entWithLines(id, typ, file string, ls, le int) codegraph.Entity {
	e := ent(id, typ, file)
	e.LineStart = ls
	e.LineEnd = le
	return e
}

func TestShortestPath_ReachableAndUnreachable(t *testing.T) {
	s, ctx := freshStore(t)

	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "sp",
		Files: []string{"a.go", "b.go", "c.go"},
		Entities: []codegraph.Entity{
			ent("sp:a", "go_func", "a.go"),
			ent("sp:b", "go_func", "b.go"),
			ent("sp:c", "go_func", "c.go"),
		},
		Edges: []codegraph.Edge{
			{From: "sp:a", To: "sp:b", Relation: "calls", SrcFile: "a.go"},
			{From: "sp:b", To: "sp:c", Relation: "calls", SrcFile: "b.go"},
		},
	})
	require.NoError(t, err)

	// Reachable: a -> b -> c
	chain, err := s.ShortestPath(ctx, "sp", "sp:a", "sp:c", []string{"calls"}, 5)
	require.NoError(t, err)
	require.Len(t, chain, 3)
	require.Equal(t, "sp:a", chain[0].ID)
	require.Equal(t, "sp:b", chain[1].ID)
	require.Equal(t, "sp:c", chain[2].ID)

	// Direct edge
	chain2, err := s.ShortestPath(ctx, "sp", "sp:a", "sp:b", []string{"calls"}, 5)
	require.NoError(t, err)
	require.Len(t, chain2, 2)
	require.Equal(t, "sp:a", chain2[0].ID)
	require.Equal(t, "sp:b", chain2[1].ID)

	// Unreachable: no path from c to a
	chain3, err := s.ShortestPath(ctx, "sp", "sp:c", "sp:a", []string{"calls"}, 5)
	require.NoError(t, err)
	require.Empty(t, chain3)

	// Unreachable: depth too shallow
	chain4, err := s.ShortestPath(ctx, "sp", "sp:a", "sp:c", []string{"calls"}, 1)
	require.NoError(t, err)
	require.Empty(t, chain4)
}

func TestImportantEntities_OrderedByDegree(t *testing.T) {
	s, ctx := freshStore(t)

	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "imp",
		Files: []string{"a.go", "b.go", "c.go"},
		Entities: []codegraph.Entity{
			ent("imp:a", "go_func", "a.go"), // degree 2: called by b and c
			ent("imp:b", "go_func", "b.go"), // degree 1: calls a
			ent("imp:c", "go_func", "c.go"), // degree 1: calls a
		},
		Edges: []codegraph.Edge{
			{From: "imp:b", To: "imp:a", Relation: "calls", SrcFile: "b.go"},
			{From: "imp:c", To: "imp:a", Relation: "calls", SrcFile: "c.go"},
		},
	})
	require.NoError(t, err)

	result, err := s.ImportantEntities(ctx, "imp", 10)
	require.NoError(t, err)
	require.NotEmpty(t, result)
	// imp:a must be first (highest degree = 2)
	require.Equal(t, "imp:a", result[0].ID)
	require.Equal(t, 2, result[0].Degree)
}

func TestStats_CountsAndIsolatedAndCycle(t *testing.T) {
	s, ctx := freshStore(t)

	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "st",
		Files: []string{"a.go", "b.go", "c.go"},
		Entities: []codegraph.Entity{
			ent("st:a", "go_func", "a.go"),
			ent("st:b", "go_func", "b.go"),
			ent("st:c", "go_func", "c.go"), // isolated
		},
		Edges: []codegraph.Edge{
			// a->b (imports, cycle: b imports a)
			{From: "st:a", To: "st:b", Relation: "imports", SrcFile: "a.go"},
			{From: "st:b", To: "st:a", Relation: "imports", SrcFile: "b.go"},
		},
	})
	require.NoError(t, err)

	stats, err := s.Stats(ctx, "st")
	require.NoError(t, err)
	require.Equal(t, 3, stats.Entities)
	require.Equal(t, 2, stats.Edges)
	require.Equal(t, map[string]int{"go_func": 3}, stats.EntitiesByType)
	require.Equal(t, map[string]int{"imports": 2}, stats.EdgesByRelation)
	require.Equal(t, 1, stats.IsolatedEntities) // st:c has no edges
	require.Equal(t, 2, stats.ImportCycles)     // a and b each can reach themselves
}

func TestAmbiguousEdges_FiltersByTierAndScore(t *testing.T) {
	s, ctx := freshStore(t)

	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "amb",
		Files: []string{"a.go"},
		Entities: []codegraph.Entity{
			ent("amb:a", "go_func", "a.go"),
			ent("amb:b", "go_func", "a.go"),
			ent("amb:c", "go_func", "a.go"),
			ent("amb:d", "go_func", "a.go"),
		},
		Edges: []codegraph.Edge{
			{From: "amb:a", To: "amb:b", Relation: "calls", SrcFile: "a.go", ConfidenceScore: 0.2, ConfidenceTier: codegraph.TierAmbiguous},
			{From: "amb:a", To: "amb:c", Relation: "calls", SrcFile: "a.go", ConfidenceScore: 0.1, ConfidenceTier: codegraph.TierAmbiguous},
			{From: "amb:a", To: "amb:d", Relation: "calls", SrcFile: "a.go", ConfidenceScore: 1.0, ConfidenceTier: codegraph.TierExtracted},
		},
	})
	require.NoError(t, err)

	edges, err := s.AmbiguousEdges(ctx, "amb", 100)
	require.NoError(t, err)
	require.Len(t, edges, 2)
	// Ordered by confidence_score asc: 0.1 first
	require.InDelta(t, 0.1, edges[0].ConfidenceScore, 1e-9)
	require.InDelta(t, 0.2, edges[1].ConfidenceScore, 1e-9)
	for _, e := range edges {
		require.Equal(t, codegraph.TierAmbiguous, e.ConfidenceTier)
	}
}

func TestConfidenceFilteredTraversal_DropsLowConfidence(t *testing.T) {
	s, ctx := freshStore(t)

	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "cf",
		Files: []string{"a.go"},
		Entities: []codegraph.Entity{
			ent("cf:a", "go_func", "a.go"),
			ent("cf:b", "go_func", "a.go"), // high confidence edge
			ent("cf:c", "go_func", "a.go"), // low confidence edge
		},
		Edges: []codegraph.Edge{
			{From: "cf:a", To: "cf:b", Relation: "calls", SrcFile: "a.go", ConfidenceScore: 0.95, ConfidenceTier: codegraph.TierInferred},
			{From: "cf:a", To: "cf:c", Relation: "calls", SrcFile: "a.go", ConfidenceScore: 0.1, ConfidenceTier: codegraph.TierAmbiguous},
		},
	})
	require.NoError(t, err)

	// Without filter: both neighbors
	all, err := s.Neighbors(ctx, "cf", "cf:a", []string{"calls"}, "out", 3, codegraph.ConfidenceFilter{})
	require.NoError(t, err)
	require.Len(t, all, 2)

	// With min_confidence=0.9: only cf:b
	filtered, err := s.Neighbors(ctx, "cf", "cf:a", []string{"calls"}, "out", 3, codegraph.ConfidenceFilter{MinConfidence: 0.9})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Equal(t, "cf:b", filtered[0].ID)

	// With tier=EXTRACTED: no edges qualify (both are INFERRED and AMBIGUOUS)
	byTier, err := s.Neighbors(ctx, "cf", "cf:a", []string{"calls"}, "out", 3, codegraph.ConfidenceFilter{Tier: codegraph.TierExtracted})
	require.NoError(t, err)
	require.Empty(t, byTier)
}

func TestRankedSearchEntities_ExactBeforeSubstring(t *testing.T) {
	s, ctx := freshStore(t)

	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "rs",
		Files: []string{"a.go"},
		Entities: []codegraph.Entity{
			{ID: "rs:FooBar", Name: "FooBar", Type: "go_func", FilePath: "a.go"}, // substring match
			{ID: "rs:Foo", Name: "Foo", Type: "go_func", FilePath: "a.go"},       // exact match
			{ID: "rs:FooSvc", Name: "FooSvc", Type: "go_func", FilePath: "a.go"}, // prefix match
		},
	})
	require.NoError(t, err)

	results, err := s.SearchEntities(ctx, "rs", "Foo", "", 10)
	require.NoError(t, err)
	require.Len(t, results, 3)
	// Exact match (rank 0) must come first
	require.Equal(t, "rs:Foo", results[0].ID)
	// Prefix match (rank 1) before substring match (rank 2)
	require.Equal(t, "rs:FooSvc", results[1].ID)
	require.Equal(t, "rs:FooBar", results[2].ID)
}

func TestEntityExplain_NeighborLabels(t *testing.T) {
	s, ctx := freshStore(t)

	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "ex",
		Files: []string{"a.go", "b.go"},
		Entities: []codegraph.Entity{
			entWithLines("ex:a", "go_func", "a.go", 1, 10),
			entWithLines("ex:b", "go_func", "b.go", 5, 20),
		},
		Edges: []codegraph.Edge{
			{From: "ex:a", To: "ex:b", Relation: "calls", SrcFile: "a.go"},
		},
	})
	require.NoError(t, err)

	ex, err := s.EntityExplain(ctx, "ex", "ex:a")
	require.NoError(t, err)
	require.Equal(t, "ex:a", ex.ID)
	require.Len(t, ex.OutEdges, 1)
	require.Len(t, ex.OutNeighbors, 1)
	require.Equal(t, "ex:b", ex.OutNeighbors[0].ID)
	require.Equal(t, "b.go", ex.OutNeighbors[0].FilePath)
	require.Equal(t, 5, ex.OutNeighbors[0].LineStart)
	require.Equal(t, 20, ex.OutNeighbors[0].LineEnd)
	require.Empty(t, ex.InNeighbors)
}

func TestEntityExplain_NotFound(t *testing.T) {
	s, ctx := freshStore(t)
	_, err := s.EntityExplain(ctx, "ex", "nope:id")
	require.ErrorIs(t, err, codegraph.ErrEntityNotFound)
}
