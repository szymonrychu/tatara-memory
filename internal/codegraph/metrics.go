package codegraph

import "github.com/prometheus/client_golang/prometheus"

// Metrics holds the code-graph domain counters.
type Metrics struct {
	entitiesUpserted *prometheus.CounterVec
	edgesUpserted    *prometheus.CounterVec
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
	}
	reg.MustRegister(m.entitiesUpserted, m.edgesUpserted)
	return m
}

func (m *Metrics) observePush(repo string, entities, edges int) {
	if m == nil {
		return
	}
	m.entitiesUpserted.WithLabelValues(repo).Add(float64(entities))
	m.edgesUpserted.WithLabelValues(repo).Add(float64(edges))
}
