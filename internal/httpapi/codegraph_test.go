package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type stubCodeGraph struct {
	pushErr   error
	pushed    codegraph.GraphPush
	entity    codegraph.EntityDetail
	entityErr error
	nodes     []codegraph.PathNode
}

func (s *stubCodeGraph) Push(_ context.Context, p codegraph.GraphPush) (codegraph.PushResult, error) {
	s.pushed = p
	if s.pushErr != nil {
		return codegraph.PushResult{}, s.pushErr
	}
	return codegraph.PushResult{Repo: p.Repo, Files: len(p.Files), EntitiesUpserted: len(p.Entities), EdgesUpserted: len(p.Edges)}, nil
}
func (s *stubCodeGraph) Search(_ context.Context, _, _, _ string, _ int) ([]codegraph.Entity, error) {
	return []codegraph.Entity{{ID: "go:func:r/a.A", Name: "A", Type: "go_func"}}, nil
}
func (s *stubCodeGraph) Entity(_ context.Context, _, _ string) (codegraph.EntityDetail, error) {
	return s.entity, s.entityErr
}
func (s *stubCodeGraph) Neighbors(_ context.Context, _, _ string, _ []string, _ string, _ int) ([]codegraph.PathNode, error) {
	return s.nodes, nil
}
func (s *stubCodeGraph) Callers(_ context.Context, _, _ string, _ int) ([]codegraph.PathNode, error) {
	return s.nodes, nil
}
func (s *stubCodeGraph) Callees(_ context.Context, _, _ string, _ int) ([]codegraph.PathNode, error) {
	return s.nodes, nil
}
func (s *stubCodeGraph) Dependents(_ context.Context, _, _ string, _ int) ([]codegraph.PathNode, error) {
	return s.nodes, nil
}
func (s *stubCodeGraph) Dependencies(_ context.Context, _, _ string, _ int) ([]codegraph.PathNode, error) {
	return s.nodes, nil
}
func (s *stubCodeGraph) ResourceGraph(_ context.Context, _, _ string, _ int) ([]codegraph.PathNode, error) {
	return s.nodes, nil
}
func (s *stubCodeGraph) FileImports(_ context.Context, _, _ string) ([]codegraph.Edge, error) {
	return []codegraph.Edge{{From: "p", To: "q", Relation: "imports"}}, nil
}

// stubMemory is a minimal MemoryService stub for use in package-internal tests.
type stubMemory struct{}

func (s *stubMemory) CreateMemory(_ context.Context, m memory.Memory) (memory.Memory, error) {
	m.ID = "stub"
	return m, nil
}
func (s *stubMemory) GetMemory(_ context.Context, _ string) (memory.Memory, error) {
	return memory.Memory{}, nil
}
func (s *stubMemory) DeleteMemory(_ context.Context, _ string) error { return nil }
func (s *stubMemory) Query(_ context.Context, _ memory.Query) (memory.QueryResult, error) {
	return memory.QueryResult{}, nil
}
func (s *stubMemory) Describe(_ context.Context, _ memory.Query) (memory.DescribeResult, error) {
	return memory.DescribeResult{}, nil
}
func (s *stubMemory) GetEntity(_ context.Context, _ string) (memory.Entity, error) {
	return memory.Entity{}, nil
}
func (s *stubMemory) SearchEntities(_ context.Context, _ string) ([]memory.Entity, error) {
	return nil, nil
}
func (s *stubMemory) PatchEntity(_ context.Context, _ string, _ memory.Entity) (memory.Entity, error) {
	return memory.Entity{}, nil
}
func (s *stubMemory) ListEdges(_ context.Context) ([]memory.Edge, error) { return nil, nil }
func (s *stubMemory) CreateEdge(_ context.Context, e memory.Edge) (memory.Edge, error) {
	e.ID = "stub"
	return e, nil
}
func (s *stubMemory) DeleteEdge(_ context.Context, _ string) error { return nil }

func cgRouter(cg CodeGraphService) http.Handler {
	return NewRouter(Config{
		Service:   &stubMemory{},
		CodeGraph: cg,
		Registry:  prometheus.NewRegistry(),
	})
}

func TestPostCodeGraph_OK(t *testing.T) {
	cg := &stubCodeGraph{}
	body := `{"repo":"r","files":["a.go"],"entities":[{"id":"x","file_path":"a.go","type":"go_func","name":"x"}],"edges":[]}`
	req := httptest.NewRequest(http.MethodPost, "/code-graph:bulk", strings.NewReader(body))
	w := httptest.NewRecorder()
	cgRouter(cg).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var res codegraph.PushResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Equal(t, 1, res.EntitiesUpserted)
	require.Equal(t, "r", cg.pushed.Repo)
}

func TestPostCodeGraph_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/code-graph:bulk", strings.NewReader("{"))
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSearchEntities_OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code/entities?repo=r&q=A", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "go:func:r/a.A")
}

func TestSearchEntities_MissingRepo(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code/entities?q=A", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetEntity_NotFound(t *testing.T) {
	cg := &stubCodeGraph{entityErr: codegraph.ErrEntityNotFound}
	req := httptest.NewRequest(http.MethodGet, "/code/entity?repo=r&id=missing", nil)
	w := httptest.NewRecorder()
	cgRouter(cg).ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestCallers_OK(t *testing.T) {
	cg := &stubCodeGraph{nodes: []codegraph.PathNode{{Entity: codegraph.Entity{ID: "go:func:r/x.X"}, Depth: 1}}}
	req := httptest.NewRequest(http.MethodGet, "/code/callers?repo=r&id=go:func:r/y.Y&depth=2", nil)
	w := httptest.NewRecorder()
	cgRouter(cg).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "go:func:r/x.X")
}

func TestNeighbors_RequiresRelation(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code/neighbors?repo=r&id=x", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestFileImports_OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code/file-imports?repo=r&path=a.go", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "imports")
}
