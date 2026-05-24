package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
)

type edgeStub struct {
	stubService
	list []httpapi.Edge
}

func (s *edgeStub) ListEdges(_ context.Context) ([]httpapi.Edge, error) { return s.list, nil }
func (s *edgeStub) CreateEdge(_ context.Context, e httpapi.Edge) (httpapi.Edge, error) {
	e.ID = "edge_new"
	return e, nil
}
func (s *edgeStub) DeleteEdge(_ context.Context, _ string) error { return nil }

func TestListEdges200(t *testing.T) {
	srv := newSrv(t, &edgeStub{list: []httpapi.Edge{{ID: "e1"}}})
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/edges")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)
}

func TestCreateEdge201(t *testing.T) {
	srv := newSrv(t, &edgeStub{})
	defer srv.Close()
	body, _ := json.Marshal(httpapi.Edge{From: "a", To: "b", Relation: "rel"})
	resp, err := http.Post(srv.URL+"/v1/edges", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestCreateEdgeMissingFields400(t *testing.T) {
	srv := newSrv(t, &edgeStub{})
	defer srv.Close()
	body, _ := json.Marshal(httpapi.Edge{From: "a"})
	resp, err := http.Post(srv.URL+"/v1/edges", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 400, resp.StatusCode)
}

func TestDeleteEdge204(t *testing.T) {
	srv := newSrv(t, &edgeStub{})
	defer srv.Close()
	req, _ := http.NewRequest("DELETE", srv.URL+"/v1/edges/e1", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}
