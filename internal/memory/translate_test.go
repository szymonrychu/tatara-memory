package memory_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestToInsertText(t *testing.T) {
	m := memory.Memory{ID: "m1", Text: "hello"}
	req := memory.ToInsertText(m)
	require.Equal(t, "hello", req.Text)
}

func TestQueryResultFromQuery_UsesReferences(t *testing.T) {
	resp := lightrag.QueryResponse{
		Response: "answer",
		References: []lightrag.ReferenceItem{
			{ReferenceID: "r1", FilePath: "/a.md", Content: []string{"chunk a", "chunk b"}},
			{ReferenceID: "r2", FilePath: "/b.md"},
		},
	}
	got := memory.QueryResultFromQuery(resp)
	require.Len(t, got.Matches, 2)
	require.Equal(t, "r1", got.Matches[0].ID)
	require.Equal(t, "chunk a\nchunk b", got.Matches[0].Text)
	require.Equal(t, "/b.md", got.Matches[1].Text)
	require.InDelta(t, 0.0, got.Matches[0].Score, 1e-6)
}

func TestDescribeResultFromQuery_CollectsFilePaths(t *testing.T) {
	resp := lightrag.QueryResponse{
		Response: "X is Y",
		References: []lightrag.ReferenceItem{
			{ReferenceID: "r1", FilePath: "/a.md"},
			{ReferenceID: "r2", FilePath: "/b.md"},
		},
	}
	got := memory.DescribeResultFromQuery(resp)
	require.Equal(t, "X is Y", got.Response)
	require.Equal(t, []string{"/a.md", "/b.md"}, got.Sources)
}

func TestMakeAndParseEdgeID(t *testing.T) {
	id := memory.MakeEdgeID("from", "to")
	require.Equal(t, "from||to", id)

	from, to, ok := memory.ParseEdgeID(id)
	require.True(t, ok)
	require.Equal(t, "from", from)
	require.Equal(t, "to", to)

	_, _, ok = memory.ParseEdgeID("malformed")
	require.False(t, ok)
}

func TestEntityFromGraphNode(t *testing.T) {
	n := lightrag.GraphNode{
		ID: "Tesla",
		Properties: map[string]any{
			"entity_type": "ORGANIZATION",
			"description": "EV maker",
			"weight":      "1.0",
		},
	}
	e := memory.EntityFromGraphNode(n)
	require.Equal(t, "Tesla", e.ID)
	require.Equal(t, "Tesla", e.Name)
	require.Equal(t, "ORGANIZATION", e.Type)
	require.Equal(t, "EV maker", e.Description)
	require.Equal(t, "1.0", e.Properties["weight"])
}

func TestEntityUpdatePayload(t *testing.T) {
	p := memory.EntityUpdatePayload(memory.Entity{
		Name:        "new",
		Type:        "T",
		Description: "d",
		Properties:  map[string]string{"k": "v"},
	})
	require.Equal(t, "new", p["entity_name"])
	require.Equal(t, "T", p["entity_type"])
	require.Equal(t, "d", p["description"])
	require.Equal(t, "v", p["k"])
}

func TestRelationCreatePayload(t *testing.T) {
	req := memory.RelationCreatePayload(memory.Edge{From: "a", To: "b", Relation: "rel", Properties: map[string]string{"w": "1"}})
	require.Equal(t, "a", req.SourceEntity)
	require.Equal(t, "b", req.TargetEntity)
	require.Equal(t, "rel", req.RelationData["keywords"])
	require.Equal(t, "1", req.RelationData["w"])
}
