package codegraph

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Query op labels for code_graph_query_total and code_graph_query_duration_seconds.
const (
	queryOpNeighbors     = "neighbors"
	queryOpShortestPath  = "shortest_path"
	queryOpStats         = "stats"
	queryOpBridges       = "bridges"
	queryOpRelated       = "related"
	queryOpEntityExplain = "entity_explain"
	queryOpImportantBy   = "important_by"
	queryOpAmbiguous     = "ambiguous"
	// Finding 4: read paths that were previously uninstrumented.
	queryOpSearch            = "search"
	queryOpEntity            = "entity"
	queryOpFileImports       = "file_imports"
	queryOpCrossRepo         = "cross_repo"
	queryOpImportantEntities = "important_entities"
	queryOpSemanticMisses    = "semantic_misses"
	queryOpHyperedges        = "hyperedges"
	queryOpHyperedge         = "hyperedge"
	queryOpCommunities       = "communities"
	queryOpCommunity         = "community"
)

var queryOps = []string{
	queryOpNeighbors,
	queryOpShortestPath,
	queryOpStats,
	queryOpBridges,
	queryOpRelated,
	queryOpEntityExplain,
	queryOpImportantBy,
	queryOpAmbiguous,
	queryOpSearch,
	queryOpEntity,
	queryOpFileImports,
	queryOpCrossRepo,
	queryOpImportantEntities,
	queryOpSemanticMisses,
	queryOpHyperedges,
	queryOpHyperedge,
	queryOpCommunities,
	queryOpCommunity,
}

// Metrics holds the code-graph domain counters.
type Metrics struct {
	entitiesUpserted *prometheus.CounterVec
	edgesUpserted    *prometheus.CounterVec
	// Query instruments (finding 4): per-operation observability for the
	// traversal/path/stats methods that run recursive-CTE queries.
	queryTotal    *prometheus.CounterVec
	queryDuration *prometheus.HistogramVec
	// pushTotal counts every attempted code-graph write by outcome
	// (tatara-memory#98). entitiesUpserted alone cannot express failure: a
	// counter that stops moving is indistinguishable from a repo nobody is
	// pushing to, which is why #98's dead write path stayed invisible for
	// seven hours with nothing an alert could be written against.
	pushTotal *prometheus.CounterVec
}

// Result labels for code_graph_push_total.
const (
	pushResultSuccess     = "success"
	pushResultLockTimeout = "lock_timeout"
	pushResultDeadlock    = "deadlock"
	pushResultTimeout     = "timeout"
	pushResultError       = "error"
)

// pushResultFor maps a Reconcile error onto its result label. The three
// classified errors get their own label precisely so an alert can distinguish
// "the write path is blocked on Postgres locks" from "a push was malformed".
func pushResultFor(err error) string {
	switch {
	case err == nil:
		return pushResultSuccess
	case errors.Is(err, ErrLockTimeout):
		return pushResultLockTimeout
	case errors.Is(err, ErrDeadlock):
		return pushResultDeadlock
	case errors.Is(err, ErrReconcileTimeout):
		return pushResultTimeout
	default:
		return pushResultError
	}
}

// NewMetrics creates and registers the code-graph metrics with reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		entitiesUpserted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "code_graph_entities_upserted_total",
			Help: "Code-graph entities upserted, by repo.",
		}, []string{"repo"}),
		edgesUpserted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "code_graph_edges_upserted_total",
			Help: "Code-graph edges upserted, by repo.",
		}, []string{"repo"}),
		queryTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "code_graph_query_total",
			Help: "Count of code-graph query/traversal operations by op and result.",
		}, []string{"op", "result"}),
		queryDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "code_graph_query_duration_seconds",
			Help:    "Duration of code-graph query/traversal operations by op.",
			Buckets: prometheus.DefBuckets,
		}, []string{"op"}),
		pushTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "code_graph_push_total",
			Help: "Count of attempted code-graph pushes by repo and outcome.",
		}, []string{"repo", "result"}),
	}
	reg.MustRegister(m.entitiesUpserted, m.edgesUpserted, m.queryTotal, m.queryDuration, m.pushTotal)
	for _, op := range queryOps {
		for _, result := range []string{"success", "error"} {
			m.queryTotal.WithLabelValues(op, result)
		}
		m.queryDuration.WithLabelValues(op)
	}
	return m
}

// observePushResult records the outcome of one attempted push. Repos are not
// pre-created here the way query ops are: the label set is discovered from
// traffic, so pre-creating every result for a repo we have never seen would
// publish zeros that no alert should treat as evidence of a healthy write path.
func (m *Metrics) observePushResult(repo string, err error) {
	if m == nil {
		return
	}
	m.pushTotal.WithLabelValues(repo, pushResultFor(err)).Inc()
}

func (m *Metrics) observePush(repo string, entities, edges int) {
	if m == nil {
		return
	}
	m.entitiesUpserted.WithLabelValues(repo).Add(float64(entities))
	m.edgesUpserted.WithLabelValues(repo).Add(float64(edges))
}

func (m *Metrics) observeQuery(op string, start time.Time, err error) {
	if m == nil {
		return
	}
	result := "success"
	if err != nil {
		result = "error"
	}
	m.queryTotal.WithLabelValues(op, result).Inc()
	m.queryDuration.WithLabelValues(op).Observe(time.Since(start).Seconds())
}
