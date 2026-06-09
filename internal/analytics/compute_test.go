package analytics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// twoClusterGraph: {a,b,c} densely connected, {d,e,f} densely connected,
// with a single bridge edge c-d. c and d should have the highest betweenness.
func twoClusterEdges() []Edge {
	return []Edge{
		{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "a", To: "c"},
		{From: "d", To: "e"}, {From: "e", To: "f"}, {From: "d", To: "f"},
		{From: "c", To: "d"}, // bridge
	}
}

func TestCompute_TwoClustersCommunitiesAndCentrality(t *testing.T) {
	res := Compute([]string{"a", "b", "c", "d", "e", "f"}, twoClusterEdges())

	// Exactly two communities.
	communities := map[int]bool{}
	for _, c := range res.Communities {
		communities[c.Community] = true
	}
	require.Len(t, communities, 2, "expected two communities")

	// a,b,c share one community; d,e,f share the other; the two differ.
	comm := map[string]int{}
	for _, n := range res.Nodes {
		comm[n.ID] = n.Community
	}
	require.Equal(t, comm["a"], comm["b"])
	require.Equal(t, comm["b"], comm["c"])
	require.Equal(t, comm["d"], comm["e"])
	require.Equal(t, comm["e"], comm["f"])
	require.NotEqual(t, comm["a"], comm["d"])

	// Degree: c and d have degree 3 (two intra + one bridge); a has degree 2.
	deg := map[string]int{}
	for _, n := range res.Nodes {
		deg[n.ID] = n.Degree
	}
	require.Equal(t, 3, deg["c"])
	require.Equal(t, 3, deg["d"])
	require.Equal(t, 2, deg["a"])

	// Betweenness: the bridge endpoints c and d are strictly higher than a.
	bw := map[string]float64{}
	for _, n := range res.Nodes {
		bw[n.ID] = n.Betweenness
	}
	require.Greater(t, bw["c"], bw["a"])
	require.Greater(t, bw["d"], bw["a"])

	// Community size + cohesion are populated.
	for _, c := range res.Communities {
		require.Equal(t, 3, c.Size)
		require.GreaterOrEqual(t, c.Cohesion, 0.0)
	}
}

func TestCompute_EmptyGraph(t *testing.T) {
	res := Compute(nil, nil)
	require.Empty(t, res.Nodes)
	require.Empty(t, res.Communities)
}

func TestCompute_IsolatedNode(t *testing.T) {
	res := Compute([]string{"x"}, nil)
	require.Len(t, res.Nodes, 1)
	require.Equal(t, "x", res.Nodes[0].ID)
	require.Equal(t, 0, res.Nodes[0].Degree)
}
