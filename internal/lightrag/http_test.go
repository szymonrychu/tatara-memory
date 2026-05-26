package lightrag_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*lightrag.HTTPClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: srv.URL})
	require.NoError(t, err)
	return c, srv
}

func TestHTTPClient_InsertText(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/documents/text", r.URL.Path)

		var in lightrag.InsertTextRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		require.Equal(t, "hello world", in.Text)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(lightrag.InsertResponse{
			Status: "success", Message: "submitted", TrackID: "track-1",
		})
	})

	resp, err := c.InsertText(context.Background(), lightrag.InsertTextRequest{Text: "hello world"})
	require.NoError(t, err)
	require.Equal(t, "track-1", resp.TrackID)
	require.Equal(t, "success", resp.Status)
}

func TestHTTPClient_InsertText_OnlyTextInBody(t *testing.T) {
	// The lightrag /documents/text endpoint rejects bodies with unexpected fields.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		require.NotContains(t, string(buf), "documents")
		require.NotContains(t, string(buf), "content")
		_ = json.NewEncoder(w).Encode(lightrag.InsertResponse{Status: "success", TrackID: "track-1"})
	})

	_, err := c.InsertText(context.Background(), lightrag.InsertTextRequest{Text: "x"})
	require.NoError(t, err)
}

func TestHTTPClient_TrackStatus(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/documents/track_status/track-1", r.URL.Path)
		_ = json.NewEncoder(w).Encode(lightrag.TrackStatusResponse{
			TrackID:    "track-1",
			TotalCount: 1,
			Documents: []lightrag.DocStatusResponse{
				{ID: "doc-1", Status: lightrag.DocStatusProcessed, ContentSummary: "hi"},
			},
			StatusSummary: map[string]int{"processed": 1},
		})
	})

	ts, err := c.TrackStatus(context.Background(), "track-1")
	require.NoError(t, err)
	require.Equal(t, "track-1", ts.TrackID)
	require.Len(t, ts.Documents, 1)
	require.Equal(t, lightrag.DocStatusProcessed, ts.Documents[0].Status)
}

func TestHTTPClient_DeleteDocs(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/documents/delete_document", r.URL.Path)

		var in lightrag.DeleteDocRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		require.Equal(t, []string{"doc-1"}, in.DocIDs)

		_ = json.NewEncoder(w).Encode(lightrag.DeleteDocByIdResponse{
			Status: "deletion_started", Message: "ok", DocID: "doc-1",
		})
	})

	resp, err := c.DeleteDocs(context.Background(), lightrag.DeleteDocRequest{DocIDs: []string{"doc-1"}})
	require.NoError(t, err)
	require.Equal(t, "deletion_started", resp.Status)
}

func TestHTTPClient_Query(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/query", r.URL.Path)

		var in lightrag.QueryRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		require.Equal(t, lightrag.QueryModeHybrid, in.Mode)
		require.Equal(t, "what is X", in.Query)

		_ = json.NewEncoder(w).Encode(lightrag.QueryResponse{
			Response: "X is Y",
			References: []lightrag.ReferenceItem{
				{ReferenceID: "ref-1", FilePath: "/path/a.md", Content: []string{"chunk a"}},
			},
		})
	})

	resp, err := c.Query(context.Background(), lightrag.QueryRequest{
		Query: "what is X", Mode: lightrag.QueryModeHybrid,
	})
	require.NoError(t, err)
	require.Equal(t, "X is Y", resp.Response)
	require.Len(t, resp.References, 1)
	require.Equal(t, "ref-1", resp.References[0].ReferenceID)
}

func TestHTTPClient_Query_RejectsInvalidMode(t *testing.T) {
	c, _ := newTestClient(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be called")
	})
	_, err := c.Query(context.Background(), lightrag.QueryRequest{Query: "x", Mode: "bogus"})
	require.Error(t, err)
}

func TestHTTPClient_Query_EmptyModeAllowed(t *testing.T) {
	// LightRAG defaults Mode to "mix" server-side; client must allow omitting it.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		require.NotContains(t, string(buf), `"mode"`)
		_ = json.NewEncoder(w).Encode(lightrag.QueryResponse{Response: "ok"})
	})
	_, err := c.Query(context.Background(), lightrag.QueryRequest{Query: "x"})
	require.NoError(t, err)
}

func TestHTTPClient_QueryData(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/query/data", r.URL.Path)
		_ = json.NewEncoder(w).Encode(lightrag.QueryDataResponse{
			Status:  "success",
			Message: "ok",
			Data:    map[string]any{"entities": []any{}},
		})
	})

	resp, err := c.QueryData(context.Background(), lightrag.QueryRequest{
		Query: "x", Mode: lightrag.QueryModeMix,
	})
	require.NoError(t, err)
	require.Equal(t, "success", resp.Status)
}

func TestHTTPClient_EntityExists(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/graph/entity/exists", r.URL.Path)
		require.Equal(t, "Tesla", r.URL.Query().Get("name"))
		_ = json.NewEncoder(w).Encode(lightrag.EntityExistsResponse{Exists: true})
	})

	exists, err := c.EntityExists(context.Background(), "Tesla")
	require.NoError(t, err)
	require.True(t, exists)
}

func TestHTTPClient_CreateEntity(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/graph/entity/create", r.URL.Path)
		var in lightrag.EntityCreateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		require.Equal(t, "Tesla", in.EntityName)

		_ = json.NewEncoder(w).Encode(lightrag.EntityResponse{
			Status: "success", Message: "created",
			Data: map[string]any{"entity_name": "Tesla"},
		})
	})
	resp, err := c.CreateEntity(context.Background(), lightrag.EntityCreateRequest{
		EntityName: "Tesla",
		EntityData: map[string]any{"description": "EV maker"},
	})
	require.NoError(t, err)
	require.Equal(t, "success", resp.Status)
}

func TestHTTPClient_UpdateEntity(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/graph/entity/edit", r.URL.Path)

		var in lightrag.EntityUpdateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		require.Equal(t, "Tesla", in.EntityName)
		require.Equal(t, "updated", in.UpdatedData["description"])

		_ = json.NewEncoder(w).Encode(lightrag.EntityResponse{Status: "success"})
	})

	_, err := c.UpdateEntity(context.Background(), lightrag.EntityUpdateRequest{
		EntityName:  "Tesla",
		UpdatedData: map[string]any{"description": "updated"},
	})
	require.NoError(t, err)
}

func TestHTTPClient_DeleteEntity(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/documents/delete_entity", r.URL.Path)

		var in lightrag.DeleteEntityRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		require.Equal(t, "Tesla", in.EntityName)
		w.WriteHeader(http.StatusNoContent)
	})

	require.NoError(t, c.DeleteEntity(context.Background(), lightrag.DeleteEntityRequest{EntityName: "Tesla"}))
}

func TestHTTPClient_LabelSearch(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/graph/label/search", r.URL.Path)
		require.Equal(t, "Te", r.URL.Query().Get("q"))
		_ = json.NewEncoder(w).Encode([]string{"Tesla", "Telecom"})
	})

	out, err := c.LabelSearch(context.Background(), "Te")
	require.NoError(t, err)
	require.Equal(t, []string{"Tesla", "Telecom"}, out)
}

func TestHTTPClient_Graph(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/graphs", r.URL.Path)
		require.Equal(t, "Tesla", r.URL.Query().Get("label"))
		require.Equal(t, "2", r.URL.Query().Get("max_depth"))
		_ = json.NewEncoder(w).Encode(lightrag.KnowledgeGraph{
			Nodes: []lightrag.GraphNode{{ID: "Tesla"}},
			Edges: []lightrag.GraphEdge{},
		})
	})

	g, err := c.Graph(context.Background(), "Tesla", 2, 0)
	require.NoError(t, err)
	require.Len(t, g.Nodes, 1)
}

func TestHTTPClient_CreateRelation(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/graph/relation/create", r.URL.Path)

		var in lightrag.RelationCreateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		require.Equal(t, "Elon Musk", in.SourceEntity)
		require.Equal(t, "Tesla", in.TargetEntity)
		require.Equal(t, "CEO", in.RelationData["keywords"])

		_ = json.NewEncoder(w).Encode(lightrag.RelationResponse{Status: "success"})
	})

	_, err := c.CreateRelation(context.Background(), lightrag.RelationCreateRequest{
		SourceEntity: "Elon Musk",
		TargetEntity: "Tesla",
		RelationData: map[string]any{"keywords": "CEO"},
	})
	require.NoError(t, err)
}

func TestHTTPClient_DeleteRelation(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/documents/delete_relation", r.URL.Path)

		var in lightrag.DeleteRelationRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		require.Equal(t, "a", in.SourceEntity)
		require.Equal(t, "b", in.TargetEntity)
		w.WriteHeader(http.StatusNoContent)
	})

	require.NoError(t, c.DeleteRelation(context.Background(), lightrag.DeleteRelationRequest{
		SourceEntity: "a", TargetEntity: "b",
	}))
}

func TestHTTPClient_Health(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/health", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})
	require.NoError(t, c.Health(context.Background()))
}

func TestHTTPClient_HTTPError_Carries404(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	})

	_, err := c.TrackStatus(context.Background(), "missing")
	require.Error(t, err)
	var he *lightrag.HTTPError
	require.ErrorAs(t, err, &he)
	require.Equal(t, 404, he.Status)
}

func TestHTTPClient_PathEscape_SlashInTrackID(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw := r.URL.RawPath
		if raw == "" {
			raw = r.URL.Path
		}
		require.True(t, strings.HasSuffix(raw, "/track%2Fa"),
			"slash in trackID must be path-escaped, got %q", raw)
		_ = json.NewEncoder(w).Encode(lightrag.TrackStatusResponse{TrackID: "track/a"})
	})
	_, _ = c.TrackStatus(context.Background(), "track/a")
}

func TestHTTPClient_AcceptHeader(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "application/json", r.Header.Get("Accept"))
		w.WriteHeader(http.StatusOK)
	})
	require.NoError(t, c.Health(context.Background()))
}
