package codegraph

import (
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
}

// Metrics holds the code-graph domain counters.
type Metrics struct {
	entitiesUpserted *prometheus.CounterVec
	edgesUpserted    *prometheus.CounterVec
	// Query instruments (finding 4): per-operation observability for the
	// traversal/path/stats methods that run recursive-CTE queries.
	queryTotal    *prometheus.CounterVec
	queryDuration *prometheus.HistogramVec
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
	}
	reg.MustRegister(m.entitiesUpserted, m.edgesUpserted, m.queryTotal, m.queryDuration)
	for _, op := range queryOps {
		for _, result := range []string{"success", "error"} {
			m.queryTotal.WithLabelValues(op, result)
		}
		m.queryDuration.WithLabelValues(op)
	}
	return m
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
