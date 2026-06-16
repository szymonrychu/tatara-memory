package httpapi_test

// Tests for round-3 audit findings in internal/httpapi.
// Each block is labeled with its finding number.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// ---- Finding 8: empty text must 400 ----

func TestPostQuery_EmptyText_Returns400(t *testing.T) {
	srv := newSrv(t, &queryStub{})
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mode": "hybrid", "text": ""})
	resp, err := http.Post(srv.URL+"/queries", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPostQuery_MissingText_Returns400(t *testing.T) {
	srv := newSrv(t, &queryStub{})
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mode": "hybrid"})
	resp, err := http.Post(srv.URL+"/queries", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPostQueryDescribe_EmptyText_Returns400(t *testing.T) {
	srv := newSrv(t, &queryStub{})
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mode": "hybrid", "text": ""})
	resp, err := http.Post(srv.URL+"/queries:describe", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// ---- Finding 1: TopK must be capped ----

// queryStubCapture records the last Query call so we can inspect TopK.
type queryStubCapture struct {
	stubService
	lastQuery memory.Query
}

func (q *queryStubCapture) Query(_ context.Context, qr memory.Query) (memory.QueryResult, error) {
	q.lastQuery = qr
	return memory.QueryResult{}, nil
}

func (q *queryStubCapture) Describe(_ context.Context, qr memory.Query) (memory.DescribeResult, error) {
	q.lastQuery = qr
	return memory.DescribeResult{}, nil
}

func TestPostQuery_TopKCappedAtMax(t *testing.T) {
	svc := &queryStubCapture{}
	srv := newSrv(t, svc)
	defer srv.Close()

	body, _ := json.Marshal(map[string]interface{}{"mode": "hybrid", "text": "x", "top_k": 100000000})
	resp, err := http.Post(srv.URL+"/queries", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.LessOrEqual(t, svc.lastQuery.TopK, 500,
		"top_k must be clamped to at most 500 before forwarding to LightRAG")
}

func TestPostQuery_NegativeTopK_Returns400(t *testing.T) {
	srv := newSrv(t, &queryStub{})
	defer srv.Close()

	body, _ := json.Marshal(map[string]interface{}{"mode": "hybrid", "text": "x", "top_k": -1})
	resp, err := http.Post(srv.URL+"/queries", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPostQueryDescribe_TopKCappedAtMax(t *testing.T) {
	svc := &queryStubCapture{}
	srv := newSrv(t, svc)
	defer srv.Close()

	body, _ := json.Marshal(map[string]interface{}{"mode": "hybrid", "text": "x", "top_k": 99999})
	resp, err := http.Post(srv.URL+"/queries:describe", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.LessOrEqual(t, svc.lastQuery.TopK, 500,
		"top_k must be clamped to at most 500 before forwarding")
}

// ---- Finding 4: 5xx errors must be logged at ERROR ----

// errSentinel is a unique error value that causes the default 500 branch.
var errSentinel = errors.New("unexpected internal failure")

type queryStubError struct {
	stubService
	err error
}

func (q *queryStubError) Query(_ context.Context, _ memory.Query) (memory.QueryResult, error) {
	return memory.QueryResult{}, q.err
}
func (q *queryStubError) Describe(_ context.Context, _ memory.Query) (memory.DescribeResult, error) {
	return memory.DescribeResult{}, q.err
}

func TestMapServiceError_5xx_LoggedAtError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	svc := &queryStubError{err: errSentinel}
	r := httpapi.NewRouter(httpapi.Config{
		Service:  svc,
		Logger:   logger,
		Registry: prometheus.NewRegistry(),
	})

	body, _ := json.Marshal(map[string]string{"mode": "hybrid", "text": "x"})
	req := httptest.NewRequest(http.MethodPost, "/queries", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)

	// Find an ERROR-level log line referencing the error.
	var found bool
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec["level"] == "ERROR" {
			found = true
			break
		}
	}
	require.True(t, found,
		"a 5xx response must emit an ERROR-level log; got logs: %s", buf.String())
}

// ---- Finding 7: double-clamping - Related with limit=0 must default via service (20), not HTTP (100) ----

// stubCodeGraphWithLimitCapture records the last limit passed to Related/Hyperedges/Communities/Community.
type stubCodeGraphWithLimitCapture struct {
	lastRelatedLimit     int
	lastHyperedgesLimit  int
	lastCommunitiesLimit int
	lastCommunityLimit   int
}

func (s *stubCodeGraphWithLimitCapture) Push(_ context.Context, _ codegraph.GraphPush) (codegraph.PushResult, error) {
	return codegraph.PushResult{}, nil
}
func (s *stubCodeGraphWithLimitCapture) Search(_ context.Context, _, _, _ string, _ int) ([]codegraph.Entity, error) {
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) Entity(_ context.Context, _, _ string) (codegraph.EntityDetail, error) {
	return codegraph.EntityDetail{}, nil
}
func (s *stubCodeGraphWithLimitCapture) Neighbors(_ context.Context, _, _ string, _ []string, _ string, _, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) Callers(_ context.Context, _, _ string, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) Callees(_ context.Context, _, _ string, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) Dependents(_ context.Context, _, _ string, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) Dependencies(_ context.Context, _, _ string, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) ResourceGraph(_ context.Context, _, _ string, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) FileImports(_ context.Context, _, _ string, _ int) ([]codegraph.Edge, error) {
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) CrossRepo(_ context.Context, _, _ string, _ int) (codegraph.CrossRepoLinks, error) {
	return codegraph.CrossRepoLinks{Consumers: []codegraph.CrossRef{}, Providers: []codegraph.CrossRef{}}, nil
}
func (s *stubCodeGraphWithLimitCapture) ShortestPath(_ context.Context, _, _, _ string, _ []string, _ int) ([]codegraph.Entity, error) {
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) ImportantEntities(_ context.Context, _ string, _ int) ([]codegraph.EntityDegree, error) {
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) Stats(_ context.Context, _ string) (codegraph.GraphStats, error) {
	return codegraph.GraphStats{EntitiesByType: map[string]int{}, EdgesByRelation: map[string]int{}, EdgesByTier: map[string]int{}}, nil
}
func (s *stubCodeGraphWithLimitCapture) AmbiguousEdges(_ context.Context, _ string, _ int) ([]codegraph.Edge, error) {
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) EntityExplain(_ context.Context, _, _ string) (codegraph.EntityExplain, error) {
	return codegraph.EntityExplain{}, nil
}
func (s *stubCodeGraphWithLimitCapture) SemanticMisses(_ context.Context, _ string, _ []codegraph.FileSHA) ([]string, error) {
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) Related(_ context.Context, _, _ string, _ []string, _ float64, limit int) ([]codegraph.RelatedResult, error) {
	s.lastRelatedLimit = limit
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) Hyperedges(_ context.Context, _, _ string, limit int) ([]codegraph.Hyperedge, error) {
	s.lastHyperedgesLimit = limit
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) Hyperedge(_ context.Context, _, _ string) (codegraph.Hyperedge, error) {
	return codegraph.Hyperedge{}, nil
}
func (s *stubCodeGraphWithLimitCapture) Communities(_ context.Context, _ string, limit int) ([]codegraph.CommunityRow, error) {
	s.lastCommunitiesLimit = limit
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) Community(_ context.Context, _ string, _ int, limit int) ([]codegraph.Entity, error) {
	s.lastCommunityLimit = limit
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) Bridges(_ context.Context, _ string, _ int) ([]codegraph.Bridge, error) {
	return nil, nil
}
func (s *stubCodeGraphWithLimitCapture) ImportantEntitiesBy(_ context.Context, _, _ string, _ int) ([]codegraph.EntityDegree, error) {
	return nil, nil
}

func limitCaptureRouter(cg httpapi.CodeGraphService) http.Handler {
	return httpapi.NewRouter(httpapi.Config{
		Service:   &stubService{},
		CodeGraph: cg,
		Registry:  prometheus.NewRegistry(),
	})
}

// TestRelated_ZeroLimit_PassesZeroToService verifies that when ?limit is absent
// the HTTP layer passes 0 (not defaultListLimit=100) to the service, so the
// service's own default of 20 applies (finding 7: no double-clamping).
func TestRelated_ZeroLimit_PassesZeroToService(t *testing.T) {
	cg := &stubCodeGraphWithLimitCapture{}
	r := limitCaptureRouter(cg)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/code-graph/related?repo=r&id=x", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 0, cg.lastRelatedLimit,
		"HTTP layer must pass raw 0 limit to Related so the service default (20) applies")
}

func TestHyperedges_ZeroLimit_PassesZeroToService(t *testing.T) {
	cg := &stubCodeGraphWithLimitCapture{}
	r := limitCaptureRouter(cg)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/code-graph/hyperedges?repo=r", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 0, cg.lastHyperedgesLimit,
		"HTTP layer must pass raw 0 limit to Hyperedges so the service default (20) applies")
}

func TestCommunities_ZeroLimit_PassesZeroToService(t *testing.T) {
	cg := &stubCodeGraphWithLimitCapture{}
	r := limitCaptureRouter(cg)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/code-graph/communities?repo=r", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 0, cg.lastCommunitiesLimit,
		"HTTP layer must pass raw 0 limit to Communities so the service default (20) applies")
}

func TestCommunity_ZeroLimit_PassesZeroToService(t *testing.T) {
	cg := &stubCodeGraphWithLimitCapture{}
	r := limitCaptureRouter(cg)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/code-graph/community?repo=r&community=0", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 0, cg.lastCommunityLimit,
		"HTTP layer must pass raw 0 limit to Community so the service default (20) applies")
}

// ---- Finding 2: FileImports limit must be pushed to the service ----

type stubFileImportsWithLimit struct {
	lastLimit int
}

func (s *stubFileImportsWithLimit) Push(_ context.Context, _ codegraph.GraphPush) (codegraph.PushResult, error) {
	return codegraph.PushResult{}, nil
}
func (s *stubFileImportsWithLimit) Search(_ context.Context, _, _, _ string, _ int) ([]codegraph.Entity, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) Entity(_ context.Context, _, _ string) (codegraph.EntityDetail, error) {
	return codegraph.EntityDetail{}, nil
}
func (s *stubFileImportsWithLimit) Neighbors(_ context.Context, _, _ string, _ []string, _ string, _, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) Callers(_ context.Context, _, _ string, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) Callees(_ context.Context, _, _ string, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) Dependents(_ context.Context, _, _ string, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) Dependencies(_ context.Context, _, _ string, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) ResourceGraph(_ context.Context, _, _ string, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) FileImports(_ context.Context, _, _ string, limit int) ([]codegraph.Edge, error) {
	s.lastLimit = limit
	return []codegraph.Edge{{From: "p", To: "q", Relation: "imports"}}, nil
}
func (s *stubFileImportsWithLimit) CrossRepo(_ context.Context, _, _ string, _ int) (codegraph.CrossRepoLinks, error) {
	return codegraph.CrossRepoLinks{Consumers: []codegraph.CrossRef{}, Providers: []codegraph.CrossRef{}}, nil
}
func (s *stubFileImportsWithLimit) ShortestPath(_ context.Context, _, _, _ string, _ []string, _ int) ([]codegraph.Entity, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) ImportantEntities(_ context.Context, _ string, _ int) ([]codegraph.EntityDegree, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) Stats(_ context.Context, _ string) (codegraph.GraphStats, error) {
	return codegraph.GraphStats{EntitiesByType: map[string]int{}, EdgesByRelation: map[string]int{}, EdgesByTier: map[string]int{}}, nil
}
func (s *stubFileImportsWithLimit) AmbiguousEdges(_ context.Context, _ string, _ int) ([]codegraph.Edge, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) EntityExplain(_ context.Context, _, _ string) (codegraph.EntityExplain, error) {
	return codegraph.EntityExplain{}, nil
}
func (s *stubFileImportsWithLimit) SemanticMisses(_ context.Context, _ string, _ []codegraph.FileSHA) ([]string, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) Related(_ context.Context, _, _ string, _ []string, _ float64, _ int) ([]codegraph.RelatedResult, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) Hyperedges(_ context.Context, _, _ string, _ int) ([]codegraph.Hyperedge, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) Hyperedge(_ context.Context, _, _ string) (codegraph.Hyperedge, error) {
	return codegraph.Hyperedge{}, nil
}
func (s *stubFileImportsWithLimit) Communities(_ context.Context, _ string, _ int) ([]codegraph.CommunityRow, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) Community(_ context.Context, _ string, _ int, _ int) ([]codegraph.Entity, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) Bridges(_ context.Context, _ string, _ int) ([]codegraph.Bridge, error) {
	return nil, nil
}
func (s *stubFileImportsWithLimit) ImportantEntitiesBy(_ context.Context, _, _ string, _ int) ([]codegraph.EntityDegree, error) {
	return nil, nil
}

func TestFileImports_LimitPushedToService(t *testing.T) {
	cg := &stubFileImportsWithLimit{}
	r := httpapi.NewRouter(httpapi.Config{
		Service:   &stubService{},
		CodeGraph: cg,
		Registry:  prometheus.NewRegistry(),
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/code/file-imports?repo=r&path=a.go&limit=42", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 42, cg.lastLimit,
		"file-imports must pass the clamped limit to the service, not do post-fetch truncation")
}

func TestFileImports_DefaultLimit_PassesClampedDefault(t *testing.T) {
	cg := &stubFileImportsWithLimit{}
	r := httpapi.NewRouter(httpapi.Config{
		Service:   &stubService{},
		CodeGraph: cg,
		Registry:  prometheus.NewRegistry(),
	})
	w := httptest.NewRecorder()
	// No ?limit param: handler should pass clampListLimit(0) = 100
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/code/file-imports?repo=r&path=a.go", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 100, cg.lastLimit,
		"file-imports with no limit must pass the default 100 to the service")
}

// ---- Finding 6: CrossRepo must accept and honour a ?limit param ----

type stubCrossRepoWithLimit struct {
	lastLimit int
}

func (s *stubCrossRepoWithLimit) Push(_ context.Context, _ codegraph.GraphPush) (codegraph.PushResult, error) {
	return codegraph.PushResult{}, nil
}
func (s *stubCrossRepoWithLimit) Search(_ context.Context, _, _, _ string, _ int) ([]codegraph.Entity, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) Entity(_ context.Context, _, _ string) (codegraph.EntityDetail, error) {
	return codegraph.EntityDetail{}, nil
}
func (s *stubCrossRepoWithLimit) Neighbors(_ context.Context, _, _ string, _ []string, _ string, _, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) Callers(_ context.Context, _, _ string, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) Callees(_ context.Context, _, _ string, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) Dependents(_ context.Context, _, _ string, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) Dependencies(_ context.Context, _, _ string, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) ResourceGraph(_ context.Context, _, _ string, _ int, _ codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) FileImports(_ context.Context, _, _ string, _ int) ([]codegraph.Edge, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) CrossRepo(_ context.Context, _, _ string, limit int) (codegraph.CrossRepoLinks, error) {
	s.lastLimit = limit
	return codegraph.CrossRepoLinks{Consumers: []codegraph.CrossRef{}, Providers: []codegraph.CrossRef{}}, nil
}
func (s *stubCrossRepoWithLimit) ShortestPath(_ context.Context, _, _, _ string, _ []string, _ int) ([]codegraph.Entity, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) ImportantEntities(_ context.Context, _ string, _ int) ([]codegraph.EntityDegree, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) Stats(_ context.Context, _ string) (codegraph.GraphStats, error) {
	return codegraph.GraphStats{EntitiesByType: map[string]int{}, EdgesByRelation: map[string]int{}, EdgesByTier: map[string]int{}}, nil
}
func (s *stubCrossRepoWithLimit) AmbiguousEdges(_ context.Context, _ string, _ int) ([]codegraph.Edge, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) EntityExplain(_ context.Context, _, _ string) (codegraph.EntityExplain, error) {
	return codegraph.EntityExplain{}, nil
}
func (s *stubCrossRepoWithLimit) SemanticMisses(_ context.Context, _ string, _ []codegraph.FileSHA) ([]string, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) Related(_ context.Context, _, _ string, _ []string, _ float64, _ int) ([]codegraph.RelatedResult, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) Hyperedges(_ context.Context, _, _ string, _ int) ([]codegraph.Hyperedge, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) Hyperedge(_ context.Context, _, _ string) (codegraph.Hyperedge, error) {
	return codegraph.Hyperedge{}, nil
}
func (s *stubCrossRepoWithLimit) Communities(_ context.Context, _ string, _ int) ([]codegraph.CommunityRow, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) Community(_ context.Context, _ string, _ int, _ int) ([]codegraph.Entity, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) Bridges(_ context.Context, _ string, _ int) ([]codegraph.Bridge, error) {
	return nil, nil
}
func (s *stubCrossRepoWithLimit) ImportantEntitiesBy(_ context.Context, _, _ string, _ int) ([]codegraph.EntityDegree, error) {
	return nil, nil
}

func TestCrossRepo_LimitPushedToService(t *testing.T) {
	cg := &stubCrossRepoWithLimit{}
	r := httpapi.NewRouter(httpapi.Config{
		Service:   &stubService{},
		CodeGraph: cg,
		Registry:  prometheus.NewRegistry(),
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/code/cross-repo?repo=r&id=e1&limit=25", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 25, cg.lastLimit,
		"cross-repo must pass the clamped limit to the service (finding 6)")
}

func TestCrossRepo_DefaultLimit_PassesClampedDefault(t *testing.T) {
	cg := &stubCrossRepoWithLimit{}
	r := httpapi.NewRouter(httpapi.Config{
		Service:   &stubService{},
		CodeGraph: cg,
		Registry:  prometheus.NewRegistry(),
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/code/cross-repo?repo=r&id=e1", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 100, cg.lastLimit,
		"cross-repo with no limit must pass the default 100 to the service")
}

// Avoid unused import.
var _ = strings.Contains
