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
	pushErr    error
	pushed     codegraph.GraphPush
	entity     codegraph.EntityDetail
	entityErr  error
	nodes      []codegraph.PathNode
	lastCF     codegraph.ConfidenceFilter
	explainErr error
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
func (s *stubCodeGraph) Neighbors(_ context.Context, _, _ string, _ []string, _ string, _ int, cf codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	s.lastCF = cf
	return s.nodes, nil
}
func (s *stubCodeGraph) Callers(_ context.Context, _, _ string, _ int, cf codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	s.lastCF = cf
	return s.nodes, nil
}
func (s *stubCodeGraph) Callees(_ context.Context, _, _ string, _ int, cf codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	s.lastCF = cf
	return s.nodes, nil
}
func (s *stubCodeGraph) Dependents(_ context.Context, _, _ string, _ int, cf codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	s.lastCF = cf
	return s.nodes, nil
}
func (s *stubCodeGraph) Dependencies(_ context.Context, _, _ string, _ int, cf codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	s.lastCF = cf
	return s.nodes, nil
}
func (s *stubCodeGraph) ResourceGraph(_ context.Context, _, _ string, _ int, cf codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	s.lastCF = cf
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
func (s *stubCodeGraph) ShortestPath(_ context.Context, _, _, _ string, _ []string, _ int) ([]codegraph.Entity, error) {
	return []codegraph.Entity{
		{ID: "go:func:r/a.A", Name: "A", Type: "go_func"},
		{ID: "go:func:r/b.B", Name: "B", Type: "go_func"},
	}, nil
}
func (s *stubCodeGraph) ImportantEntities(_ context.Context, _ string, _ int) ([]codegraph.EntityDegree, error) {
	return []codegraph.EntityDegree{
		{Entity: codegraph.Entity{ID: "go:func:r/a.A", Name: "A", Type: "go_func"}, Degree: 5},
	}, nil
}
func (s *stubCodeGraph) Stats(_ context.Context, _ string) (codegraph.GraphStats, error) {
	return codegraph.GraphStats{
		Entities:        3,
		Edges:           2,
		EntitiesByType:  map[string]int{"go_func": 3},
		EdgesByRelation: map[string]int{"calls": 2},
		EdgesByTier:     map[string]int{codegraph.TierExtracted: 2},
	}, nil
}
func (s *stubCodeGraph) AmbiguousEdges(_ context.Context, _ string, _ int) ([]codegraph.Edge, error) {
	return []codegraph.Edge{
		{From: "a", To: "b", Relation: "calls", ConfidenceScore: 0.2, ConfidenceTier: codegraph.TierAmbiguous},
	}, nil
}
func (s *stubCodeGraph) EntityExplain(_ context.Context, _, _ string) (codegraph.EntityExplain, error) {
	if s.explainErr != nil {
		return codegraph.EntityExplain{}, s.explainErr
	}
	return codegraph.EntityExplain{
		EntityDetail: codegraph.EntityDetail{
			Entity:   codegraph.Entity{ID: "go:func:r/a.A", Name: "A", Type: "go_func"},
			OutEdges: []codegraph.Edge{},
			InEdges:  []codegraph.Edge{},
		},
		OutNeighbors: []codegraph.NeighborEntity{{ID: "go:func:r/b.B", Name: "B", Type: "go_func"}},
		InNeighbors:  []codegraph.NeighborEntity{},
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

func TestShortestPath_OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code-graph/path?repo=r&from=go:func:r/a.A&to=go:func:r/b.B", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var res map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	path, ok := res["path"].([]interface{})
	require.True(t, ok)
	require.Len(t, path, 2)
}

func TestShortestPath_MissingFrom(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code-graph/path?repo=r&to=b", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestShortestPath_MissingTo(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code-graph/path?repo=r&from=a", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestImportantEntities_OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code-graph/important?repo=r&limit=5", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "degree")
	require.Contains(t, w.Body.String(), "go:func:r/a.A")
}

func TestStats_OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code-graph/stats?repo=r", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var stats codegraph.GraphStats
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &stats))
	require.Equal(t, 3, stats.Entities)
	require.Equal(t, 2, stats.Edges)
}

func TestStats_MissingRepo(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code-graph/stats", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAmbiguousEdges_OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code-graph/ambiguous?repo=r", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "AMBIGUOUS")
}

func TestEntityExplain_OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code-graph/explain?repo=r&id=go:func:r/a.A", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "out_neighbors")
	require.Contains(t, w.Body.String(), "go:func:r/b.B")
}

func TestEntityExplain_NotFound(t *testing.T) {
	cg := &stubCodeGraph{explainErr: codegraph.ErrEntityNotFound}
	req := httptest.NewRequest(http.MethodGet, "/code-graph/explain?repo=r&id=missing", nil)
	w := httptest.NewRecorder()
	cgRouter(cg).ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestConfidenceFilter_InvalidTier(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code/callers?repo=r&id=x&tier=INVALID", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestConfidenceFilter_ValidTier(t *testing.T) {
	cg := &stubCodeGraph{nodes: []codegraph.PathNode{{Entity: codegraph.Entity{ID: "go:func:r/x.X"}, Depth: 1}}}
	req := httptest.NewRequest(http.MethodGet, "/code/callers?repo=r&id=x&tier=EXTRACTED&min_confidence=0.9", nil)
	w := httptest.NewRecorder()
	cgRouter(cg).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, codegraph.TierExtracted, cg.lastCF.Tier)
	require.InDelta(t, 0.9, cg.lastCF.MinConfidence, 1e-9)
}

func TestConfidenceFilter_InvalidMinConfidence(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code/callers?repo=r&id=x&min_confidence=notanumber", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestConfidenceFilter_MinConfidenceBelowZero(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code/callers?repo=r&id=x&min_confidence=-0.1", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestConfidenceFilter_MinConfidenceAboveOne(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/code/callers?repo=r&id=x&min_confidence=1.1", nil)
	w := httptest.NewRecorder()
	cgRouter(&stubCodeGraph{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func (s *stubCodeGraph) SemanticMisses(_ context.Context, _ string, files []codegraph.FileSHA) ([]string, error) {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out, nil
}

func (s *stubCodeGraph) Related(_ context.Context, _, _ string, _ []string, _ float64) ([]codegraph.RelatedResult, error) {
	return []codegraph.RelatedResult{{Entity: codegraph.Entity{ID: "rel:b", Name: "B"}, Relation: codegraph.RelConceptuallyRelated, ConfidenceScore: 0.9}}, nil
}

func (s *stubCodeGraph) Hyperedges(_ context.Context, _, _ string) ([]codegraph.Hyperedge, error) {
	return []codegraph.Hyperedge{{ID: "h1", Label: "flow", Members: []string{"a", "b", "c"}}}, nil
}

func (s *stubCodeGraph) Hyperedge(_ context.Context, _, _ string) (codegraph.Hyperedge, error) {
	return codegraph.Hyperedge{ID: "h1", Label: "flow", Members: []string{"a", "b", "c"}}, nil
}

func (s *stubCodeGraph) Communities(_ context.Context, _ string) ([]codegraph.CommunityRow, error) {
	return []codegraph.CommunityRow{{Community: 0, Label: "auth", Size: 3, Cohesion: 1.0}}, nil
}

func (s *stubCodeGraph) Community(_ context.Context, _ string, _ int) ([]codegraph.Entity, error) {
	return []codegraph.Entity{{ID: "cm:a", Name: "A"}}, nil
}

func (s *stubCodeGraph) Bridges(_ context.Context, _ string, _ int) ([]codegraph.Bridge, error) {
	return []codegraph.Bridge{{Entity: codegraph.Entity{ID: "cm:c", Name: "C"}, Betweenness: 5.0, NeighborCommunities: 2}}, nil
}

func (s *stubCodeGraph) ImportantEntitiesBy(_ context.Context, _, by string, _ int) ([]codegraph.EntityDegree, error) {
	return []codegraph.EntityDegree{{Entity: codegraph.Entity{ID: "imp:" + by}, Degree: 3}}, nil
}

func newCodeGraphRouter(t *testing.T) http.Handler {
	t.Helper()
	return httpapi.NewRouter(httpapi.Config{
		Service:   &stubService{},
		CodeGraph: &stubCodeGraph{},
		Registry:  prometheus.NewRegistry(),
	})
}

func TestRoute_Related(t *testing.T) {
	r := newCodeGraphRouter(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/code-graph/related?repo=r&id=rel:a&min_confidence=0.5", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Related []codegraph.RelatedResult `json:"related"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Related, 1)
	require.Equal(t, "rel:b", body.Related[0].ID)
}

func TestRoute_Related_BadMinConfidence(t *testing.T) {
	r := newCodeGraphRouter(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/code-graph/related?repo=r&id=x&min_confidence=2", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRoute_Hyperedges(t *testing.T) {
	r := newCodeGraphRouter(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/code-graph/hyperedges?repo=r", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Hyperedges []codegraph.Hyperedge `json:"hyperedges"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Hyperedges, 1)
}

func TestRoute_Hyperedge(t *testing.T) {
	r := newCodeGraphRouter(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/code-graph/hyperedge?repo=r&id=h1", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var h codegraph.Hyperedge
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &h))
	require.Equal(t, "h1", h.ID)
}

func TestRoute_SemanticMisses(t *testing.T) {
	r := newCodeGraphRouter(t)
	rec := httptest.NewRecorder()
	payload := `{"repo":"r","files":[{"path":"a.go","content_sha":"s1"},{"path":"b.go","content_sha":"s2"}]}`
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/code-graph/semantic-misses", strings.NewReader(payload)))
	require.Equal(t, http.StatusOK, rec.Code)
	var misses []string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &misses))
	require.ElementsMatch(t, []string{"a.go", "b.go"}, misses)
}

func TestRoute_Communities(t *testing.T) {
	r := newCodeGraphRouter(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/code-graph/communities?repo=r", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Communities []codegraph.CommunityRow `json:"communities"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Communities, 1)
	require.Equal(t, "auth", body.Communities[0].Label)
}

func TestRoute_Community(t *testing.T) {
	r := newCodeGraphRouter(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/code-graph/community?repo=r&community=0", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Entities []codegraph.Entity `json:"entities"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Entities, 1)
}

func TestRoute_Bridges(t *testing.T) {
	r := newCodeGraphRouter(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/code-graph/bridges?repo=r&limit=5", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Bridges []codegraph.Bridge `json:"bridges"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Bridges, 1)
	require.Equal(t, "cm:c", body.Bridges[0].ID)
}

func TestRoute_ImportantBy(t *testing.T) {
	r := newCodeGraphRouter(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/code-graph/important?repo=r&by=betweenness", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Entities []codegraph.EntityDegree `json:"entities"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Entities, 1)
	require.Equal(t, "imp:betweenness", body.Entities[0].ID)
}

func TestConfidenceFilter_MinConfidenceBoundary(t *testing.T) {
	// 0.0 and 1.0 are valid boundaries
	for _, v := range []string{"0.0", "1.0", "0.5"} {
		cg := &stubCodeGraph{nodes: []codegraph.PathNode{{Entity: codegraph.Entity{ID: "go:func:r/x.X"}, Depth: 1}}}
		req := httptest.NewRequest(http.MethodGet, "/code/callers?repo=r&id=x&min_confidence="+v, nil)
		w := httptest.NewRecorder()
		cgRouter(cg).ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "min_confidence=%s should be accepted", v)
	}
}
