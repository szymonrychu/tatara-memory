package lightrag_test

import (
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
