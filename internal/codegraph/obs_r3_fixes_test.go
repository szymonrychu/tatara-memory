package codegraph

// Tests for obs-scaffold round-3 finding 4 in internal/codegraph.
// Finding 4: Search, Entity, FileImports, CrossRepo, ImportantEntities, SemanticMisses,
// Hyperedges, Hyperedge, Communities, Community must all emit observeQuery instrumentation.

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func queryCountFor(t *testing.T, reg *prometheus.Registry, op, result string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	return queryCounterFromMFs(t, mfs, op, result)
}

func queryCounterFromMFs(t *testing.T, mfs []*dto.MetricFamily, op, result string) float64 {
	t.Helper()
	for _, mf := range mfs {
		if mf.GetName() != "code_graph_query_total" {
			continue
		}
		for _, m := range mf.Metric {
			var opOK, resOK bool
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "op" && lp.GetValue() == op {
					opOK = true
				}
				if lp.GetName() == "result" && lp.GetValue() == result {
					resOK = true
				}
			}
			if opOK && resOK {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func newSvcForObs() (*Service, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	svc := NewService(&fakeStoreForMetrics{}, m)
	return svc, reg
}

func TestService_ObserveQuery_Search(t *testing.T) {
	svc, reg := newSvcForObs()
	_, err := svc.Search(context.Background(), "r", "q", "", 10)
	require.NoError(t, err)
	require.InDelta(t, 1.0, queryCountFor(t, reg, "search", "success"), 0.001,
		"Search must increment code_graph_query_total{op=search,result=success}")
}

func TestService_ObserveQuery_Entity(t *testing.T) {
	svc, reg := newSvcForObs()
	_, err := svc.Entity(context.Background(), "r", "id")
	require.NoError(t, err)
	require.InDelta(t, 1.0, queryCountFor(t, reg, "entity", "success"), 0.001,
		"Entity must increment code_graph_query_total{op=entity}")
}

func TestService_ObserveQuery_FileImports(t *testing.T) {
	svc, reg := newSvcForObs()
	_, err := svc.FileImports(context.Background(), "r", "f.go")
	require.NoError(t, err)
	require.InDelta(t, 1.0, queryCountFor(t, reg, "file_imports", "success"), 0.001,
		"FileImports must increment code_graph_query_total{op=file_imports}")
}

func TestService_ObserveQuery_CrossRepo(t *testing.T) {
	svc, reg := newSvcForObs()
	_, err := svc.CrossRepo(context.Background(), "r", "id")
	require.NoError(t, err)
	require.InDelta(t, 1.0, queryCountFor(t, reg, "cross_repo", "success"), 0.001,
		"CrossRepo must increment code_graph_query_total{op=cross_repo}")
}

func TestService_ObserveQuery_ImportantEntities(t *testing.T) {
	svc, reg := newSvcForObs()
	_, err := svc.ImportantEntities(context.Background(), "r", 10)
	require.NoError(t, err)
	require.InDelta(t, 1.0, queryCountFor(t, reg, "important_entities", "success"), 0.001,
		"ImportantEntities must increment code_graph_query_total{op=important_entities}")
}

func TestService_ObserveQuery_SemanticMisses(t *testing.T) {
	svc, reg := newSvcForObs()
	_, err := svc.SemanticMisses(context.Background(), "r", nil)
	require.NoError(t, err)
	require.InDelta(t, 1.0, queryCountFor(t, reg, "semantic_misses", "success"), 0.001,
		"SemanticMisses must increment code_graph_query_total{op=semantic_misses}")
}

func TestService_ObserveQuery_Hyperedges(t *testing.T) {
	svc, reg := newSvcForObs()
	_, err := svc.Hyperedges(context.Background(), "r", "", 10)
	require.NoError(t, err)
	require.InDelta(t, 1.0, queryCountFor(t, reg, "hyperedges", "success"), 0.001,
		"Hyperedges must increment code_graph_query_total{op=hyperedges}")
}

func TestService_ObserveQuery_Hyperedge(t *testing.T) {
	svc, reg := newSvcForObs()
	_, err := svc.Hyperedge(context.Background(), "r", "id")
	require.NoError(t, err)
	require.InDelta(t, 1.0, queryCountFor(t, reg, "hyperedge", "success"), 0.001,
		"Hyperedge must increment code_graph_query_total{op=hyperedge}")
}

func TestService_ObserveQuery_Communities(t *testing.T) {
	svc, reg := newSvcForObs()
	_, err := svc.Communities(context.Background(), "r", 10)
	require.NoError(t, err)
	require.InDelta(t, 1.0, queryCountFor(t, reg, "communities", "success"), 0.001,
		"Communities must increment code_graph_query_total{op=communities}")
}

func TestService_ObserveQuery_Community(t *testing.T) {
	svc, reg := newSvcForObs()
	_, err := svc.Community(context.Background(), "r", 0, 10)
	require.NoError(t, err)
	require.InDelta(t, 1.0, queryCountFor(t, reg, "community", "success"), 0.001,
		"Community must increment code_graph_query_total{op=community}")
}

// Verify that all new ops are pre-initialized in the registry (finding 4 completeness check).
func TestMetrics_NewQueryOpsPreInitialized(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = NewMetrics(reg)
	mfs, err := reg.Gather()
	require.NoError(t, err)

	newOps := []string{
		"search", "entity", "file_imports", "cross_repo",
		"important_entities", "semantic_misses",
		"hyperedges", "hyperedge", "communities", "community",
	}
	for _, op := range newOps {
		for _, result := range []string{"success", "error"} {
			v := queryCounterFromMFs(t, mfs, op, result)
			// Zero-value is correct; we just need the label combo to exist.
			_ = v // presence check: Gather() returned it without error
			// Actual existence: try to find the metric family.
			var found bool
			for _, mf := range mfs {
				if mf.GetName() != "code_graph_query_total" {
					continue
				}
				for _, m := range mf.Metric {
					var opOK, resOK bool
					for _, lp := range m.GetLabel() {
						if lp.GetName() == "op" && lp.GetValue() == op {
							opOK = true
						}
						if lp.GetName() == "result" && lp.GetValue() == result {
							resOK = true
						}
					}
					if opOK && resOK {
						found = true
					}
				}
			}
			require.True(t, found, "op=%s result=%s must be pre-initialized in NewMetrics", op, result)
		}
	}
}

// Compile-time reference to ensure time is used (avoids unused import error
// if all tests call methods directly without measuring time here).
var _ = time.Now
