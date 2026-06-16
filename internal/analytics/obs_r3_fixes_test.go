package analytics

// Tests for obs-scaffold round-3 finding 9 in internal/analytics.
// Finding 9: Result must expose RawEdgeCount (total input edges before filtering)
// alongside EdgeCount (kept edges) so callers can detect large drops caused by
// cross-repo/self-loop/duplicate edges.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompute_RawEdgeCountVsEdgeCount(t *testing.T) {
	ids := []string{"a", "b", "c"}
	edges := []Edge{
		{From: "a", To: "b"}, // kept
		{From: "b", To: "a"}, // duplicate in undirected graph (HasEdgeBetween), filtered
		{From: "a", To: "a"}, // self-loop, filtered
		{From: "x", To: "b"}, // x not in ids, filtered
	}
	r := Compute(ids, edges, Config{})
	require.Equal(t, 4, r.RawEdgeCount,
		"RawEdgeCount must be len(edges) regardless of filtering")
	require.Equal(t, 1, r.EdgeCount,
		"EdgeCount must only count edges that passed the filter")
	require.Greater(t, r.RawEdgeCount, r.EdgeCount,
		"gap between RawEdgeCount and EdgeCount signals filtered-out edges")
}

func TestCompute_RawEdgeCountEqualsEdgeCountWhenAllKept(t *testing.T) {
	ids := []string{"a", "b", "c"}
	edges := []Edge{
		{From: "a", To: "b"},
		{From: "b", To: "c"},
	}
	r := Compute(ids, edges, Config{})
	require.Equal(t, 2, r.RawEdgeCount)
	require.Equal(t, 2, r.EdgeCount,
		"when all edges pass the filter RawEdgeCount == EdgeCount")
}

func TestCompute_EmptyEdges_BothZero(t *testing.T) {
	r := Compute([]string{"a"}, nil, Config{})
	require.Equal(t, 0, r.RawEdgeCount)
	require.Equal(t, 0, r.EdgeCount)
}
