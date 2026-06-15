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

func TestFake_InsertAndTrackStatus(t *testing.T) {
	f := fake.New()
	resp, err := f.InsertText(context.Background(), lightrag.InsertTextRequest{Text: "hello"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.TrackID)

	ts, err := f.TrackStatus(context.Background(), resp.TrackID)
	require.NoError(t, err)
	require.Equal(t, 1, ts.TotalCount)
	require.Equal(t, "hello", ts.Documents[0].ContentSummary)
	require.Equal(t, lightrag.DocStatusProcessed, ts.Documents[0].Status)
}

func TestFake_DeleteDocs(t *testing.T) {
	f := fake.New()
	resp, _ := f.InsertText(context.Background(), lightrag.InsertTextRequest{Text: "x"})
	ts, _ := f.TrackStatus(context.Background(), resp.TrackID)
	docID := ts.Documents[0].ID

	_, err := f.DeleteDocs(context.Background(), lightrag.DeleteDocRequest{DocIDs: []string{docID}})
	require.NoError(t, err)

	_, err = f.TrackStatus(context.Background(), resp.TrackID)
	require.Error(t, err)
}

func TestFake_EntityRoundTrip(t *testing.T) {
	f := fake.New()
	_, err := f.CreateEntity(context.Background(), lightrag.EntityCreateRequest{
		EntityName: "Tesla",
		EntityData: map[string]any{"description": "EV maker"},
	})
	require.NoError(t, err)

	exists, err := f.EntityExists(context.Background(), "Tesla")
	require.NoError(t, err)
	require.True(t, exists)

	_, err = f.UpdateEntity(context.Background(), lightrag.EntityUpdateRequest{
		EntityName:  "Tesla",
		UpdatedData: map[string]any{"description": "Updated"},
	})
	require.NoError(t, err)

	g, err := f.Graph(context.Background(), "Tesla", 1, 0)
	require.NoError(t, err)
	require.Equal(t, "Updated", g.Nodes[0].Properties["description"])

	require.NoError(t, f.DeleteEntity(context.Background(), lightrag.DeleteEntityRequest{EntityName: "Tesla"}))
	exists, _ = f.EntityExists(context.Background(), "Tesla")
	require.False(t, exists)
}

func TestFake_RelationRoundTrip(t *testing.T) {
	f := fake.New()
	_, _ = f.CreateEntity(context.Background(), lightrag.EntityCreateRequest{EntityName: "Musk", EntityData: nil})
	_, _ = f.CreateEntity(context.Background(), lightrag.EntityCreateRequest{EntityName: "Tesla", EntityData: nil})

	_, err := f.CreateRelation(context.Background(), lightrag.RelationCreateRequest{
		SourceEntity: "Musk", TargetEntity: "Tesla",
		RelationData: map[string]any{"keywords": "CEO"},
	})
	require.NoError(t, err)

	g, err := f.Graph(context.Background(), "Musk", 1, 0)
	require.NoError(t, err)
	require.Len(t, g.Edges, 1)
	require.Equal(t, "Tesla", g.Edges[0].Target)

	require.NoError(t, f.DeleteRelation(context.Background(), lightrag.DeleteRelationRequest{
		SourceEntity: "Musk", TargetEntity: "Tesla",
	}))
}

func TestFake_LabelSearch(t *testing.T) {
	f := fake.New()
	f.SeedLabels([]string{"Tesla", "Telecom", "Ford"})
	out, err := f.LabelSearch(context.Background(), "te")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"Tesla", "Telecom"}, out)
}

func TestFake_SeedQueryResponse_Roundtrip(t *testing.T) {
	f := fake.New()
	f.SeedQueryResponse(lightrag.QueryResponse{
		Response: "answer",
		References: []lightrag.ReferenceItem{
			{ReferenceID: "r1", FilePath: "/a.md"},
		},
	})
	resp, err := f.Query(context.Background(), lightrag.QueryRequest{Query: "x", Mode: lightrag.QueryModeHybrid})
	require.NoError(t, err)
	require.Equal(t, "answer", resp.Response)
	require.Len(t, resp.References, 1)
}

func TestFake_SeedQueryData_Roundtrip(t *testing.T) {
	f := fake.New()
	f.SeedQueryDataResponse(lightrag.QueryDataResponse{Status: "success", Data: map[string]any{"k": "v"}})
	resp, err := f.QueryData(context.Background(), lightrag.QueryRequest{Query: "x"})
	require.NoError(t, err)
	require.Equal(t, "success", resp.Status)
	require.Equal(t, "v", resp.Data["k"])
}

// Finding 4: SetLeaveEdgesOnDelete mirrors real backend's async edge semantics.
func TestFake_DeleteEntity_LeavesEdgesWhenFlagSet(t *testing.T) {
	f := fake.New()
	f.SetLeaveEdgesOnDelete(true)

	_, _ = f.CreateEntity(context.Background(), lightrag.EntityCreateRequest{EntityName: "A", EntityData: nil})
	_, _ = f.CreateEntity(context.Background(), lightrag.EntityCreateRequest{EntityName: "B", EntityData: nil})
	_, _ = f.CreateRelation(context.Background(), lightrag.RelationCreateRequest{
		SourceEntity: "A", TargetEntity: "B", RelationData: nil,
	})

	require.NoError(t, f.DeleteEntity(context.Background(), lightrag.DeleteEntityRequest{EntityName: "A"}))

	// Entity must be gone.
	exists, _ := f.EntityExists(context.Background(), "A")
	require.False(t, exists)

	// Incident edge must still be present (dangling, mirroring async backend).
	g, err := f.Graph(context.Background(), "B", 1, 0)
	require.NoError(t, err)
	require.Len(t, g.Edges, 1, "dangling edge must survive when leaveEdgesOnDelete=true")
}

// Finding 4: Default behaviour (leaveEdgesOnDelete=false) still removes edges for convenience.
func TestFake_DeleteEntity_RemovesEdgesByDefault(t *testing.T) {
	f := fake.New()

	_, _ = f.CreateEntity(context.Background(), lightrag.EntityCreateRequest{EntityName: "X", EntityData: nil})
	_, _ = f.CreateEntity(context.Background(), lightrag.EntityCreateRequest{EntityName: "Y", EntityData: nil})
	_, _ = f.CreateRelation(context.Background(), lightrag.RelationCreateRequest{
		SourceEntity: "X", TargetEntity: "Y", RelationData: nil,
	})

	require.NoError(t, f.DeleteEntity(context.Background(), lightrag.DeleteEntityRequest{EntityName: "X"}))

	g, err := f.Graph(context.Background(), "Y", 1, 0)
	require.NoError(t, err)
	require.Empty(t, g.Edges, "incident edges must be removed by default")
}

// Finding 5: SetInsertStatus(pending) lets callers exercise the not-yet-processed lifecycle.
func TestFake_InsertText_PendingStatusLifecycle(t *testing.T) {
	f := fake.New()
	f.SetInsertStatus(lightrag.DocStatusPending)

	resp, err := f.InsertText(context.Background(), lightrag.InsertTextRequest{Text: "async doc"})
	require.NoError(t, err)

	ts, err := f.TrackStatus(context.Background(), resp.TrackID)
	require.NoError(t, err)
	require.Equal(t, 1, ts.TotalCount)
	require.Equal(t, lightrag.DocStatusPending, ts.Documents[0].Status,
		"InsertText with pending mode must return doc in pending state")
}

// Finding 5: Default behaviour still produces processed docs.
func TestFake_InsertText_DefaultIsProcessed(t *testing.T) {
	f := fake.New()
	resp, err := f.InsertText(context.Background(), lightrag.InsertTextRequest{Text: "x"})
	require.NoError(t, err)

	ts, _ := f.TrackStatus(context.Background(), resp.TrackID)
	require.Equal(t, lightrag.DocStatusProcessed, ts.Documents[0].Status)
}
