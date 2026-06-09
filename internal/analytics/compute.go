// Package analytics computes graph-theoretic signals (community membership,
// cohesion, degree, betweenness) over a repo's code graph using gonum. It is
// pure: callers load the edge list from the store, Compute returns the signals,
// and callers persist them. No DB access lives here.
package analytics

import (
	"gonum.org/v1/gonum/graph"
	"gonum.org/v1/gonum/graph/community"
	"gonum.org/v1/gonum/graph/network"
	"gonum.org/v1/gonum/graph/simple"
)

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
	Members   []string // entity IDs, for top-degree labeling by callers
}

// Result bundles the computed node and community signals.
type Result struct {
	Nodes       []NodeSignal
	Communities []CommunitySignal
}

// Compute builds an undirected graph from ids+edges and returns community,
// degree, and betweenness signals. ids that appear in no edge are still emitted
// (isolated nodes, degree 0). Empty input returns an empty Result.
func Compute(ids []string, edges []Edge) Result {
	if len(ids) == 0 {
		return Result{}
	}

	g := simple.NewUndirectedGraph()
	idToNode := make(map[string]int64, len(ids))
	nodeToID := make(map[int64]string, len(ids))
	for i, id := range ids {
		nid := int64(i)
		idToNode[id] = nid
		nodeToID[nid] = id
		g.AddNode(simple.Node(nid))
	}

	degree := make(map[string]int, len(ids))
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
		degree[e.From]++
		degree[e.To]++
	}

	// Louvain community detection (resolution 1.0, deterministic with nil src).
	reduced := community.Modularize(g, 1.0, nil)
	comms := reduced.Communities()

	betweenness := network.Betweenness(g)

	nodeCommunity := make(map[string]int, len(ids))
	commMembers := make(map[int][]string)
	for ci, members := range comms {
		for _, n := range members {
			id := nodeToID[n.ID()]
			nodeCommunity[id] = ci
			commMembers[ci] = append(commMembers[ci], id)
		}
	}

	var nodes []NodeSignal
	for _, id := range ids {
		nodes = append(nodes, NodeSignal{
			ID:          id,
			Community:   nodeCommunity[id],
			Degree:      degree[id],
			Betweenness: betweenness[idToNode[id]],
		})
	}

	var communities []CommunitySignal
	for ci, members := range commMembers {
		communities = append(communities, CommunitySignal{
			Community: ci,
			Size:      len(members),
			Cohesion:  cohesion(g, comms[ci]),
			Members:   members,
		})
	}

	return Result{Nodes: nodes, Communities: communities}
}

// cohesion is the intra-community edge density: 2*(internal edges) / (n*(n-1)).
// A fully-connected community scores 1.0; a community with no internal edges 0.
func cohesion(g *simple.UndirectedGraph, members []graph.Node) float64 {
	n := len(members)
	if n < 2 {
		return 0
	}
	internal := 0
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if g.HasEdgeBetween(members[i].ID(), members[j].ID()) {
				internal++
			}
		}
	}
	possible := float64(n*(n-1)) / 2.0
	return float64(internal) / possible
}
