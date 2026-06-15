package analytics

import (
	"sort"
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
	res := Compute([]string{"a", "b", "c", "d", "e", "f"}, twoClusterEdges(), Config{})

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
	res := Compute(nil, nil, Config{})
	require.Empty(t, res.Nodes)
	require.Empty(t, res.Communities)
}

func TestCompute_IsolatedNode(t *testing.T) {
	res := Compute([]string{"x"}, nil, Config{})
	require.Len(t, res.Nodes, 1)
	require.Equal(t, "x", res.Nodes[0].ID)
	require.Equal(t, 0, res.Nodes[0].Degree)
}

// TestCompute_Deterministic verifies that two runs on the same graph produce
// identical Results (community IDs, membership, betweenness).
func TestCompute_Deterministic(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e", "f"}
	edges := twoClusterEdges()
	r1 := Compute(ids, edges, Config{})
	r2 := Compute(ids, edges, Config{})

	require.Equal(t, r1.Nodes, r2.Nodes, "Nodes must be identical across runs")
	require.Equal(t, r1.Communities, r2.Communities, "Communities must be identical across runs")
}

// TestCompute_CommunitiesOrderedByID verifies outer slice is sorted by Community id.
func TestCompute_CommunitiesOrderedByID(t *testing.T) {
	res := Compute([]string{"a", "b", "c", "d", "e", "f"}, twoClusterEdges(), Config{})
	ids := make([]int, len(res.Communities))
	for i, c := range res.Communities {
		ids[i] = c.Community
	}
	require.True(t, sort.IntsAreSorted(ids), "Communities slice must be sorted by Community id")
}

// TestCompute_BetweennessNormalized verifies betweenness is in [0,1].
func TestCompute_BetweennessNormalized(t *testing.T) {
	res := Compute([]string{"a", "b", "c", "d", "e", "f"}, twoClusterEdges(), Config{})
	for _, n := range res.Nodes {
		require.GreaterOrEqual(t, n.Betweenness, 0.0, "betweenness must be >= 0")
		require.LessOrEqual(t, n.Betweenness, 1.0, "betweenness must be <= 1")
	}
}

// TestCompute_MaxNodesSkipsBetweenness verifies that when MaxNodes is set and
// the graph exceeds it, betweenness values are all zero (skipped).
func TestCompute_MaxNodesSkipsBetweenness(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e", "f"}
	// MaxNodes=3 < 6 nodes -> betweenness must be skipped
	res := Compute(ids, twoClusterEdges(), Config{MaxNodes: 3})
	for _, n := range res.Nodes {
		require.Equal(t, 0.0, n.Betweenness, "betweenness must be 0 when skipped")
	}
}

// TestCompute_CohesionOE verifies cohesion is correct for a fully-connected triangle.
func TestCompute_CohesionFullyConnected(t *testing.T) {
	// Single community triangle: all three pair-edges present.
	res := Compute([]string{"a", "b", "c"}, []Edge{
		{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "a", To: "c"},
	}, Config{})
	require.Len(t, res.Communities, 1)
	// 3 nodes, 3 edges, max possible 3 -> cohesion = 1.0
	require.InDelta(t, 1.0, res.Communities[0].Cohesion, 1e-9)
}

// TestCompute_DegreeViaGonum verifies degree is read from the graph (not a hand map).
func TestCompute_DegreeViaGonum(t *testing.T) {
	// a-b-c chain: a and c have degree 1, b has degree 2.
	res := Compute([]string{"a", "b", "c"}, []Edge{
		{From: "a", To: "b"}, {From: "b", To: "c"},
	}, Config{})
	deg := map[string]int{}
	for _, n := range res.Nodes {
		deg[n.ID] = n.Degree
	}
	require.Equal(t, 1, deg["a"])
	require.Equal(t, 2, deg["b"])
	require.Equal(t, 1, deg["c"])
}
