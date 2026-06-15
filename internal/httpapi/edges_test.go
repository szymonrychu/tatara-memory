package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type edgeStub struct {
	stubService
	list []memory.Edge
}

func (s *edgeStub) ListEdges(_ context.Context) ([]memory.Edge, error) { return s.list, nil }
func (s *edgeStub) CreateEdge(_ context.Context, e memory.Edge) (memory.Edge, error) {
	e.ID = "edge_new"
	return e, nil
}
func (s *edgeStub) DeleteEdge(_ context.Context, _ string) error { return nil }

func TestListEdges200(t *testing.T) {
	srv := newSrv(t, &edgeStub{list: []memory.Edge{{ID: "e1"}}})
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/edges")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)
}

func TestCreateEdge201(t *testing.T) {
	srv := newSrv(t, &edgeStub{})
	defer srv.Close()
	body, _ := json.Marshal(memory.Edge{From: "a", To: "b", Relation: "rel"})
	resp, err := http.Post(srv.URL+"/edges", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestCreateEdgeMissingFields400(t *testing.T) {
	srv := newSrv(t, &edgeStub{})
	defer srv.Close()
	body, _ := json.Marshal(memory.Edge{From: "a"})
	resp, err := http.Post(srv.URL+"/edges", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 400, resp.StatusCode)
}

func TestDeleteEdge204(t *testing.T) {
	srv := newSrv(t, &edgeStub{})
	defer srv.Close()
	req, _ := http.NewRequest("DELETE", srv.URL+"/edges/e1", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// TestCreateEdgeLogsActor verifies that POST /edges emits a structured INFO log
// with action=create_edge and a user field.
func TestCreateEdgeLogsActor(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	r := httpapi.NewRouter(httpapi.Config{Service: &edgeStub{}, Logger: logger})
	srv := httptest.NewServer(r)
	defer srv.Close()

	body, _ := json.Marshal(memory.Edge{From: "a", To: "b", Relation: "rel"})
	resp, err := http.Post(srv.URL+"/edges", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var actionLine map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		if m["action"] == "create_edge" {
			actionLine = m
			break
		}
	}
	require.NotNil(t, actionLine, "create_edge INFO log not emitted")
	_, hasUser := actionLine["user"]
	require.True(t, hasUser, "create_edge log must include user field")
	_, hasResource := actionLine["resource_id"]
	require.True(t, hasResource, "create_edge log must include resource_id field")
}

// TestDeleteEdgeLogsActor verifies that DELETE /edges/{id} emits a structured
// INFO log with action=delete_edge and a user field (actor scoping requirement).
func TestDeleteEdgeLogsActor(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	r := httpapi.NewRouter(httpapi.Config{Service: &edgeStub{}, Logger: logger})
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest("DELETE", srv.URL+"/edges/e1", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	var actionLine map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		if m["action"] == "delete_edge" {
			actionLine = m
			break
		}
	}
	require.NotNil(t, actionLine, "delete_edge INFO log not emitted")
	_, hasUser := actionLine["user"]
	require.True(t, hasUser, "delete_edge log must include user field")
}
