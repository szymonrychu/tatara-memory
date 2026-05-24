package lightrag

import "github.com/prometheus/client_golang/prometheus"

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
	for _, op := range []string{"insert_document", "get_document", "delete_document", "query", "query_describe", "list_entities", "get_entity", "update_entity", "list_edges", "create_edge", "delete_edge", "health"} {
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
