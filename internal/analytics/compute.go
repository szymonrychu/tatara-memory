// Package analytics computes graph-theoretic signals (community membership,
// cohesion, degree, betweenness) over a repo's code graph using gonum. It is
// pure: callers load the edge list from the store, Compute returns the signals,
// and callers persist them. No DB access lives here.
package analytics

import (
	"log/slog"
	"math/rand/v2"
	"sort"
	"time"

	gonumgraph "gonum.org/v1/gonum/graph"
	"gonum.org/v1/gonum/graph/community"
	"gonum.org/v1/gonum/graph/network"
	"gonum.org/v1/gonum/graph/simple"
)

// fixedSeed is the deterministic seed for Louvain community detection.
// Using a fixed seed makes community IDs stable across recomputes for the same
// graph, preventing spurious churn in code_entities.community.
const fixedSeed uint64 = 42

// Edge is one directed code-graph edge by entity ID. Direction is ignored for
// community detection and degree; the underlying graph is treated as undirected.
type Edge struct {
	From string
	To   string
}

// NodeSignal is the computed analytics for one entity.
type NodeSignal struct {
	ID          string
	Community   int
	Degree      int
	Betweenness float64
}

// CommunitySignal is the per-community summary.
type CommunitySignal struct {
	Community int
	Size      int
	Cohesion  float64
	Members   []string // entity IDs; first non-empty member name used as fallback label
}

// Config controls optional behaviour in Compute.
type Config struct {
	// MaxNodes is the maximum graph size for which betweenness centrality is
	// computed. Graphs larger than this value skip betweenness (all values left
	// at 0.0) to avoid unbounded O(V*E) Brandes runs. Zero means no limit.
	MaxNodes int
}

// Result bundles the computed node and community signals.
type Result struct {
	Nodes       []NodeSignal
	Communities []CommunitySignal
}

// Compute builds an undirected graph from ids+edges and returns community,
// degree, and betweenness signals. ids that appear in no edge are still emitted
// (isolated nodes, degree 0). Empty input returns an empty Result.
//
// Community detection is deterministic: a fixed PCG seed is passed to Louvain.
// Betweenness is normalized to [0,1] by (n-1)*(n-2)/2 so values are comparable
// across repos of different sizes. When cfg.MaxNodes > 0 and len(ids) exceeds
// it, betweenness is skipped and left at 0.0.
// The returned Communities slice is sorted by Community id.
func Compute(ids []string, edges []Edge, cfg Config) Result {
	if len(ids) == 0 {
		return Result{}
	}

	start := time.Now()

	g := simple.NewUndirectedGraph()
	idToNode := make(map[string]int64, len(ids))
	nodeToID := make(map[int64]string, len(ids))
	for i, id := range ids {
		nid := int64(i)
		idToNode[id] = nid
		nodeToID[nid] = id
		g.AddNode(simple.Node(nid))
	}

	edgeCount := 0
	for _, e := range edges {
		fn, okf := idToNode[e.From]
		tn, okt := idToNode[e.To]
		if !okf || !okt || fn == tn {
			continue
		}
		if g.HasEdgeBetween(fn, tn) {
			continue
		}
		g.SetEdge(simple.Edge{F: simple.Node(fn), T: simple.Node(tn)})
		edgeCount++
	}

	// Louvain community detection with a fixed seed for deterministic output.
	src := rand.NewPCG(fixedSeed, 0)
	reduced := community.Modularize(g, 1.0, src)
	comms := reduced.Communities()

	// Betweenness: skip for large graphs to bound CPU; normalize to [0,1].
	n := len(ids)
	betweenness := map[int64]float64{}
	betweennessSkipped := cfg.MaxNodes > 0 && n > cfg.MaxNodes
	if !betweennessSkipped {
		raw := network.Betweenness(g)
		// gonum.Betweenness sums over ordered (directed) pairs, so for an
		// undirected graph the maximum achievable value is (n-1)*(n-2).
		// Dividing by that factor yields normalized betweenness in [0,1].
		// For n < 3 every node has betweenness 0, so no normalization needed.
		norm := 1.0
		if n >= 3 {
			norm = float64((n - 1) * (n - 2))
		}
		for nid, v := range raw {
			betweenness[nid] = v / norm
		}
	}

	nodeCommunity := make(map[string]int, len(ids))
	commMembers := make(map[int][]string)
	for ci, members := range comms {
		for _, nd := range members {
			id := nodeToID[nd.ID()]
			nodeCommunity[id] = ci
			commMembers[ci] = append(commMembers[ci], id)
		}
	}

	var nodes []NodeSignal
	for _, id := range ids {
		nodes = append(nodes, NodeSignal{
			ID:          id,
			Community:   nodeCommunity[id],
			Degree:      g.From(idToNode[id]).Len(),
			Betweenness: betweenness[idToNode[id]],
		})
	}

	// Sort community keys for deterministic Communities slice order.
	keys := make([]int, 0, len(commMembers))
	for ci := range commMembers {
		keys = append(keys, ci)
	}
	sort.Ints(keys)

	communities := make([]CommunitySignal, 0, len(keys))
	for _, ci := range keys {
		members := commMembers[ci]
		communities = append(communities, CommunitySignal{
			Community: ci,
			Size:      len(members),
			Cohesion:  cohesion(g, comms[ci]),
			Members:   members,
		})
	}

	durationMs := time.Since(start).Milliseconds()
	slog.Info("analytics.Compute",
		"nodes", n,
		"edges", edgeCount,
		"communities", len(communities),
		"betweenness_skipped", betweennessSkipped,
		"duration_ms", durationMs,
	)

	return Result{Nodes: nodes, Communities: communities}
}

// cohesion is the intra-community edge density: 2*(internal edges) / (n*(n-1)).
// A fully-connected community scores 1.0; a community with no internal edges 0.
// Uses O(sum of degrees) traversal rather than O(n^2) pair enumeration.
func cohesion(g *simple.UndirectedGraph, members []gonumgraph.Node) float64 {
	n := len(members)
	if n < 2 {
		return 0
	}
	memberSet := make(map[int64]struct{}, n)
	for _, m := range members {
		memberSet[m.ID()] = struct{}{}
	}
	internal := 0
	for _, m := range members {
		it := g.From(m.ID())
		for it.Next() {
			if _, ok := memberSet[it.Node().ID()]; ok {
				internal++
			}
		}
	}
	// Each edge counted twice (once per endpoint).
	internal /= 2
	possible := float64(n*(n-1)) / 2.0
	return float64(internal) / possible
}
