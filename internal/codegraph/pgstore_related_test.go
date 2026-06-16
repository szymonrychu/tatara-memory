//go:build integration

package codegraph_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

func TestRelated_SemanticEdgesWithConfidence(t *testing.T) {
	s, _, ctx := freshStoreWithDB(t)

	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:      "rel",
		Extractor: codegraph.ExtractorSemantic,
		Files:     []string{"a.go"},
		Entities: []codegraph.Entity{
			{ID: "rel:a", Name: "A", Type: "go_func", FilePath: "a.go"},
			{ID: "rel:b", Name: "B", Type: "go_func", FilePath: "a.go"},
			{ID: "rel:c", Name: "C", Type: "go_func", FilePath: "a.go"},
		},
		Edges: []codegraph.Edge{
			{From: "rel:a", To: "rel:b", Relation: codegraph.RelConceptuallyRelated, SrcFile: "a.go", ConfidenceScore: 0.9, ConfidenceTier: codegraph.TierInferred},
			{From: "rel:a", To: "rel:c", Relation: codegraph.RelSemanticallySimilar, SrcFile: "a.go", ConfidenceScore: 0.4, ConfidenceTier: codegraph.TierInferred},
		},
	})
	require.NoError(t, err)

	// All semantic relations, min_confidence 0 -> both targets.
	all, err := s.Related(ctx, "rel", "rel:a", nil, 0, 100)
	require.NoError(t, err)
	require.Len(t, all, 2)

	// min_confidence 0.5 -> only rel:b survives.
	hi, err := s.Related(ctx, "rel", "rel:a", nil, 0.5, 100)
	require.NoError(t, err)
	require.Len(t, hi, 1)
	require.Equal(t, "rel:b", hi[0].Entity.ID)
	require.Equal(t, codegraph.RelConceptuallyRelated, hi[0].Relation)
	require.InDelta(t, 0.9, hi[0].ConfidenceScore, 1e-6)

	// relation filter narrows to one relation.
	sim, err := s.Related(ctx, "rel", "rel:a", []string{codegraph.RelSemanticallySimilar}, 0, 100)
	require.NoError(t, err)
	require.Len(t, sim, 1)
	require.Equal(t, "rel:c", sim[0].Entity.ID)
}

func TestHyperedges_AndHyperedge(t *testing.T) {
	s, _, ctx := freshStoreWithDB(t)

	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:      "hy",
		Extractor: codegraph.ExtractorSemantic,
		Files:     []string{"a.go"},
		Entities: []codegraph.Entity{
			{ID: "hy:a", Name: "A", Type: "go_func", FilePath: "a.go"},
			{ID: "hy:b", Name: "B", Type: "go_func", FilePath: "a.go"},
			{ID: "hy:c", Name: "C", Type: "go_func", FilePath: "a.go"},
		},
		Hyperedges: []codegraph.Hyperedge{
			{ID: "hy:auth-flow", Label: "auth flow", Relation: "participate_in", SrcFile: "a.go", Members: []string{"hy:a", "hy:b", "hy:c"}},
		},
	})
	require.NoError(t, err)

	// All hyperedges in repo.
	all, err := s.Hyperedges(ctx, "hy", "", 100)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, "hy:auth-flow", all[0].ID)
	require.ElementsMatch(t, []string{"hy:a", "hy:b", "hy:c"}, all[0].Members)

	// Filter by entity membership.
	byEnt, err := s.Hyperedges(ctx, "hy", "hy:b", 100)
	require.NoError(t, err)
	require.Len(t, byEnt, 1)

	none, err := s.Hyperedges(ctx, "hy", "hy:zzz", 100)
	require.NoError(t, err)
	require.Empty(t, none)

	// Single hyperedge by id.
	one, err := s.Hyperedge(ctx, "hy", "hy:auth-flow")
	require.NoError(t, err)
	require.Equal(t, "auth flow", one.Label)
	require.Len(t, one.Members, 3)

	_, err = s.Hyperedge(ctx, "hy", "nope")
	require.ErrorIs(t, err, codegraph.ErrEntityNotFound)
}
