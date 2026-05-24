package fake_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
)

func TestFake_ImplementsClient(t *testing.T) {
	var _ lightrag.Client = fake.New()
}

func TestFake_InsertAndGetDocument(t *testing.T) {
	f := fake.New()
	resp, err := f.InsertDocument(context.Background(), lightrag.InsertRequest{
		Documents: []lightrag.Document{{Content: "hello"}},
	})
	require.NoError(t, err)
	require.Len(t, resp.IDs, 1)

	doc, err := f.GetDocument(context.Background(), resp.IDs[0])
	require.NoError(t, err)
	require.Equal(t, "hello", doc.Content)
}

func TestFake_DeleteDocument(t *testing.T) {
	f := fake.New()
	resp, _ := f.InsertDocument(context.Background(), lightrag.InsertRequest{
		Documents: []lightrag.Document{{Content: "x"}},
	})
	require.NoError(t, f.DeleteDocument(context.Background(), resp.IDs[0]))
	_, err := f.GetDocument(context.Background(), resp.IDs[0])
	require.Error(t, err)
}

func TestFake_EntityRoundTrip(t *testing.T) {
	f := fake.New()
	f.SeedEntity(lightrag.Entity{ID: "e1", Name: "foo", Type: "concept"})

	got, err := f.GetEntity(context.Background(), "e1")
	require.NoError(t, err)
	require.Equal(t, "foo", got.Name)

	rename := "renamed"
	upd, err := f.UpdateEntity(context.Background(), "e1", lightrag.EntityUpdate{Name: &rename})
	require.NoError(t, err)
	require.Equal(t, "renamed", upd.Name)

	list, err := f.ListEntities(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func TestFake_EdgeRoundTrip(t *testing.T) {
	f := fake.New()
	e, err := f.CreateEdge(context.Background(), lightrag.Edge{
		FromEntity: "e1", ToEntity: "e2", Relation: "knows",
	})
	require.NoError(t, err)
	require.NotEmpty(t, e.ID)

	list, err := f.ListEdges(context.Background())
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, f.DeleteEdge(context.Background(), e.ID))
	list, _ = f.ListEdges(context.Background())
	require.Empty(t, list)
}

func TestFake_Query_ReturnsSeededMatches(t *testing.T) {
	f := fake.New()
	f.SeedMatches([]lightrag.Match{{ID: "m1", Score: 0.5, Text: "hi"}})

	resp, err := f.Query(context.Background(), lightrag.QueryRequest{
		Query: "x", Mode: lightrag.QueryModeHybrid,
	})
	require.NoError(t, err)
	require.Len(t, resp.Matches, 1)
}
