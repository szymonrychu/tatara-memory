//go:build integration

package codegraph_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

func TestReadMethods(t *testing.T) {
	s, ctx := freshStore(t)

	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "r",
		Files: []string{"a.go", "b.go", "c.go"},
		Entities: []codegraph.Entity{
			ent("go:func:r/a.A", "go_func", "a.go"),
			ent("go:func:r/b.B", "go_func", "b.go"),
			ent("go:func:r/c.C", "go_func", "c.go"),
		},
		Edges: []codegraph.Edge{
			{From: "go:func:r/a.A", To: "go:func:r/b.B", Relation: "calls", SrcFile: "a.go"},
			{From: "go:func:r/b.B", To: "go:func:r/c.C", Relation: "calls", SrcFile: "b.go"},
			{From: "go:func:r/c.C", To: "go:func:r/d.D", Relation: "calls", SrcFile: "c.go"},
		},
	})
	require.NoError(t, err)

	found, err := s.SearchEntities(ctx, "r", "B", "go_func", 10)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "go:func:r/b.B", found[0].ID)

	det, err := s.GetEntity(ctx, "r", "go:func:r/b.B")
	require.NoError(t, err)
	require.Equal(t, "go:func:r/b.B", det.ID)
	require.Len(t, det.OutEdges, 1)
	require.Len(t, det.InEdges, 1)

	_, err = s.GetEntity(ctx, "r", "go:func:r/nope")
	require.ErrorIs(t, err, codegraph.ErrEntityNotFound)

	noCF := codegraph.ConfidenceFilter{}
	out, err := s.Neighbors(ctx, "r", "go:func:r/a.A", []string{"calls"}, "out", 3, 1000, noCF)
	require.NoError(t, err)
	ids := map[string]int{}
	for _, n := range out {
		ids[n.ID] = n.Depth
	}
	require.Equal(t, 1, ids["go:func:r/b.B"])
	require.Equal(t, 2, ids["go:func:r/c.C"])
	require.NotContains(t, ids, "go:func:r/d.D")

	in, err := s.Neighbors(ctx, "r", "go:func:r/c.C", []string{"calls"}, "in", 3, 1000, noCF)
	require.NoError(t, err)
	inIDs := map[string]bool{}
	for _, n := range in {
		inIDs[n.ID] = true
	}
	require.True(t, inIDs["go:func:r/b.B"])
	require.True(t, inIDs["go:func:r/a.A"])

	d1, err := s.Neighbors(ctx, "r", "go:func:r/a.A", []string{"calls"}, "out", 1, 1000, noCF)
	require.NoError(t, err)
	require.Len(t, d1, 1)
	require.Equal(t, "go:func:r/b.B", d1[0].ID)
}
