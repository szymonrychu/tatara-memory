package lightrag

import "github.com/prometheus/client_golang/prometheus"

// Op name constants used by HTTPClient call sites and metrics pre-init.
const (
	OpInsertDocument = "insert_document"
	OpGetDocument    = "get_document"
	OpDeleteDocument = "delete_document"
	OpQuery          = "query"
	OpQueryDescribe  = "query_describe"
	OpListEntities   = "list_entities"
	OpGetEntity      = "get_entity"
	OpUpdateEntity   = "update_entity"
	OpListEdges      = "list_edges"
	OpCreateEdge     = "create_edge"
	OpDeleteEdge     = "delete_edge"
	OpHealth         = "health"
)

var allOps = []string{
	OpInsertDocument,
	OpGetDocument,
	OpDeleteDocument,
	OpQuery,
	OpQueryDescribe,
	OpListEntities,
	OpGetEntity,
	OpUpdateEntity,
	OpListEdges,
	OpCreateEdge,
	OpDeleteEdge,
	OpHealth,
}

type metrics struct {
	calls    *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		calls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lightrag_calls_total",
			Help: "Count of LightRAG client calls by op and result.",
		}, []string{"op", "result"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lightrag_call_duration_seconds",
			Help:    "Duration of LightRAG client calls by op.",
			Buckets: prometheus.DefBuckets,
		}, []string{"op"}),
	}
	if reg != nil {
		reg.MustRegister(m.calls, m.duration)
	}
	// Pre-initialize label combinations so Gather() returns families even with zero calls.
	for _, op := range allOps {
		for _, result := range []string{"success", "error"} {
			m.calls.WithLabelValues(op, result)
		}
		m.duration.WithLabelValues(op)
	}
	return m
}

func (m *metrics) observe(op string, dur float64, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	m.calls.WithLabelValues(op, result).Inc()
	m.duration.WithLabelValues(op).Observe(dur)
}

func (m *metrics) incError(op string) {
	m.calls.WithLabelValues(op, "error").Inc()
}
