//go:build integration

package codegraph_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

func TestCommunitiesAndBridgesAndImportantBy(t *testing.T) {
	s, _, ctx := freshStoreWithDB(t)

	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "cm",
		Files: []string{"f.go"},
		Entities: []codegraph.Entity{
			ent("cm:a", "go_func", "f.go"), ent("cm:b", "go_func", "f.go"), ent("cm:c", "go_func", "f.go"),
			ent("cm:d", "go_func", "f.go"), ent("cm:e", "go_func", "f.go"), ent("cm:f", "go_func", "f.go"),
		},
		Edges: []codegraph.Edge{
			{From: "cm:a", To: "cm:b", Relation: "calls", SrcFile: "f.go"},
			{From: "cm:b", To: "cm:c", Relation: "calls", SrcFile: "f.go"},
			{From: "cm:a", To: "cm:c", Relation: "calls", SrcFile: "f.go"},
			{From: "cm:d", To: "cm:e", Relation: "calls", SrcFile: "f.go"},
			{From: "cm:e", To: "cm:f", Relation: "calls", SrcFile: "f.go"},
			{From: "cm:d", To: "cm:f", Relation: "calls", SrcFile: "f.go"},
			{From: "cm:c", To: "cm:d", Relation: "calls", SrcFile: "f.go"},
		},
	})
	require.NoError(t, err)
	_, err = s.RecomputeAnalytics(ctx, "cm", nil)
	require.NoError(t, err)

	// Communities list.
	comms, err := s.Communities(ctx, "cm")
	require.NoError(t, err)
	require.Len(t, comms, 2)
	for _, c := range comms {
		require.Equal(t, 3, c.Size)
	}

	// Community members.
	members, err := s.Community(ctx, "cm", comms[0].Community)
	require.NoError(t, err)
	require.Len(t, members, 3)

	// Bridges: high-betweenness entities connecting >1 community (cm:c, cm:d).
	bridges, err := s.Bridges(ctx, "cm", 10)
	require.NoError(t, err)
	require.NotEmpty(t, bridges)
	ids := map[string]bool{}
	for _, b := range bridges {
		ids[b.ID] = true
	}
	require.True(t, ids["cm:c"] || ids["cm:d"], "a bridge endpoint must be reported")

	// ImportantBy degree vs betweenness both return data.
	byDeg, err := s.ImportantEntitiesBy(ctx, "cm", "degree", 10)
	require.NoError(t, err)
	require.NotEmpty(t, byDeg)
	byBw, err := s.ImportantEntitiesBy(ctx, "cm", "betweenness", 10)
	require.NoError(t, err)
	require.NotEmpty(t, byBw)
	// Top by betweenness is a bridge endpoint.
	require.True(t, byBw[0].ID == "cm:c" || byBw[0].ID == "cm:d")
}
