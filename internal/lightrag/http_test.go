package lightrag_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestHTTPClient_InsertDocument(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/documents", r.URL.Path)
		var in lightrag.InsertRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		require.Len(t, in.Documents, 1)
		require.Equal(t, "hello world", in.Documents[0].Content)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(lightrag.InsertResponse{IDs: []string{"doc-1"}})
	})

	resp, err := c.InsertDocument(context.Background(), lightrag.InsertRequest{
		Documents: []lightrag.Document{{Content: "hello world"}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"doc-1"}, resp.IDs)
}

func TestHTTPClient_DeleteDocument(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/documents/doc-1", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})

	require.NoError(t, c.DeleteDocument(context.Background(), "doc-1"))
}

func TestHTTPClient_GetDocument(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/documents/doc-1", r.URL.Path)
		_ = json.NewEncoder(w).Encode(lightrag.Document{ID: "doc-1", Content: "hi"})
	})

	doc, err := c.GetDocument(context.Background(), "doc-1")
	require.NoError(t, err)
	require.Equal(t, "doc-1", doc.ID)
	require.Equal(t, "hi", doc.Content)
}

func TestHTTPClient_GetDocument_404(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	})

	_, err := c.GetDocument(context.Background(), "missing")
	require.Error(t, err)
	var he *lightrag.HTTPError
	require.ErrorAs(t, err, &he)
	require.Equal(t, 404, he.Status)
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
			Matches: []lightrag.Match{{ID: "m1", Score: 0.9, Text: "X is Y"}},
		})
	})

	resp, err := c.Query(context.Background(), lightrag.QueryRequest{
		Query: "what is X", Mode: lightrag.QueryModeHybrid,
	})
	require.NoError(t, err)
	require.Len(t, resp.Matches, 1)
	require.Equal(t, "m1", resp.Matches[0].ID)
}

func TestHTTPClient_QueryDescribe(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/query/describe", r.URL.Path)
		_ = json.NewEncoder(w).Encode(lightrag.DescribeResponse{
			Response: "X is Y because Z",
			Sources:  []string{"doc-1", "doc-2"},
		})
	})

	resp, err := c.QueryDescribe(context.Background(), lightrag.QueryRequest{
		Query: "explain X", Mode: lightrag.QueryModeGlobal,
	})
	require.NoError(t, err)
	require.Equal(t, "X is Y because Z", resp.Response)
	require.Len(t, resp.Sources, 2)
}

func TestHTTPClient_Query_RejectsInvalidMode(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be called")
	})
	_, err := c.Query(context.Background(), lightrag.QueryRequest{Query: "x", Mode: "bogus"})
	require.Error(t, err)
}

func TestHTTPClient_ListEntities(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/entities", r.URL.Path)
		require.Equal(t, "foo", r.URL.Query().Get("q"))
		_ = json.NewEncoder(w).Encode([]lightrag.Entity{
			{ID: "e1", Name: "foo", Type: "concept"},
		})
	})
	ents, err := c.ListEntities(context.Background(), "foo")
	require.NoError(t, err)
	require.Len(t, ents, 1)
	require.Equal(t, "foo", ents[0].Name)
}

func TestHTTPClient_GetEntity(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/entities/e1", r.URL.Path)
		_ = json.NewEncoder(w).Encode(lightrag.Entity{ID: "e1", Name: "foo"})
	})
	e, err := c.GetEntity(context.Background(), "e1")
	require.NoError(t, err)
	require.Equal(t, "e1", e.ID)
}

func TestHTTPClient_UpdateEntity(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPatch, r.Method)
		require.Equal(t, "/entities/e1", r.URL.Path)
		var upd lightrag.EntityUpdate
		require.NoError(t, json.NewDecoder(r.Body).Decode(&upd))
		require.NotNil(t, upd.Name)
		require.Equal(t, "renamed", *upd.Name)
		_ = json.NewEncoder(w).Encode(lightrag.Entity{ID: "e1", Name: "renamed"})
	})

	name := "renamed"
	e, err := c.UpdateEntity(context.Background(), "e1", lightrag.EntityUpdate{Name: &name})
	require.NoError(t, err)
	require.Equal(t, "renamed", e.Name)
}

func TestHTTPClient_ListEdges(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/edges", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]lightrag.Edge{
			{ID: "edge-1", FromEntity: "e1", ToEntity: "e2", Relation: "knows"},
		})
	})
	edges, err := c.ListEdges(context.Background())
	require.NoError(t, err)
	require.Len(t, edges, 1)
}

func TestHTTPClient_CreateEdge(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/edges", r.URL.Path)
		var in lightrag.Edge
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		require.Equal(t, "knows", in.Relation)
		in.ID = "edge-1"
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(in)
	})

	e, err := c.CreateEdge(context.Background(), lightrag.Edge{
		FromEntity: "e1", ToEntity: "e2", Relation: "knows",
	})
	require.NoError(t, err)
	require.Equal(t, "edge-1", e.ID)
}

func TestHTTPClient_DeleteEdge(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/edges/edge-1", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})
	require.NoError(t, c.DeleteEdge(context.Background(), "edge-1"))
}

func TestHTTPClient_Health(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/health", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})
	require.NoError(t, c.Health(context.Background()))
}

func TestHTTPClient_InsertDocument_NoCreatedAt(t *testing.T) {
	// A Document with no CreatedAt set must NOT include "created_at" in the JSON body.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		body := buf.String()
		require.NotContains(t, body, "created_at",
			"created_at must be absent when CreatedAt is nil")
		_ = json.NewEncoder(w).Encode(lightrag.InsertResponse{IDs: []string{"doc-1"}})
	})

	_, err := c.InsertDocument(context.Background(), lightrag.InsertRequest{
		Documents: []lightrag.Document{{Content: "no timestamp"}},
	})
	require.NoError(t, err)
}

func TestHTTPClient_PathEscape_SlashInID(t *testing.T) {
	// An ID containing "/" must be percent-encoded in the URL path.
	// net/http decodes r.URL.Path, so we check r.URL.RawPath for the wire encoding.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw := r.URL.RawPath
		if raw == "" {
			raw = r.URL.Path // no encoding was needed; fallback
		}
		require.Equal(t, "/documents/a%2Fb", raw,
			"slash in ID must be path-escaped to %%2F")
		w.WriteHeader(http.StatusNoContent)
	})

	_ = c.DeleteDocument(context.Background(), "a/b")
}

func TestHTTPClient_AcceptHeader(t *testing.T) {
	// Every request must carry Accept: application/json.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "application/json", r.Header.Get("Accept"))
		w.WriteHeader(http.StatusNoContent)
	})

	_ = c.DeleteDocument(context.Background(), "doc-1")
}
