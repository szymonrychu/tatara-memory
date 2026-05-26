package lightrag

import "github.com/prometheus/client_golang/prometheus"

// Op name constants used by HTTPClient call sites and metrics pre-init.
const (
	OpInsertText     = "insert_text"
	OpTrackStatus    = "track_status"
	OpDeleteDocs     = "delete_docs"
	OpQuery          = "query"
	OpQueryData      = "query_data"
	OpEntityExists   = "entity_exists"
	OpCreateEntity   = "create_entity"
	OpUpdateEntity   = "update_entity"
	OpDeleteEntity   = "delete_entity"
	OpLabelSearch    = "label_search"
	OpGraph          = "graph"
	OpCreateRelation = "create_relation"
	OpDeleteRelation = "delete_relation"
	OpHealth         = "health"
)

var allOps = []string{
	OpInsertText,
	OpTrackStatus,
	OpDeleteDocs,
	OpQuery,
	OpQueryData,
	OpEntityExists,
	OpCreateEntity,
	OpUpdateEntity,
	OpDeleteEntity,
	OpLabelSearch,
	OpGraph,
	OpCreateRelation,
	OpDeleteRelation,
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
