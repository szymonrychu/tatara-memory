package httpapi_test

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
	"github.com/szymonrychu/tatara-memory/internal/httpapi"
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
func (s *stubCodeGraph) CrossRepo(_ context.Context, _, _ string) (codegraph.CrossRepoLinks, error) {
	return codegraph.CrossRepoLinks{
		Consumers: []codegraph.CrossRef{{Repo: "repo-b", EntityID: "eb1", Symbol: "Foo", Lang: "go"}},
		Providers: []codegraph.CrossRef{},
	}, nil
}

func cgRouter(cg httpapi.CodeGraphService) http.Handler {
	return httpapi.NewRouter(httpapi.Config{
		Service:   &stubService{},
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

func TestCrossRepo_OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code/cross-repo?repo=r&id=e1", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var links codegraph.CrossRepoLinks
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &links))
	require.NotNil(t, links.Consumers)
	require.NotNil(t, links.Providers)
}

func TestCrossRepo_MissingRepo(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code/cross-repo?id=e1", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCrossRepo_MissingID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code/cross-repo?repo=r", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}
