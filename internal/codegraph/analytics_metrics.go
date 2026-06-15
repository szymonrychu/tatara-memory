package codegraph

import "github.com/prometheus/client_golang/prometheus"

// Result classes for code_graph_analytics_runs_total.
const (
	analyticsResultSuccess = "success"
	analyticsResultError   = "error"
)

// AnalyticsMetrics holds the Prometheus instruments for the async analytics
// recompute worker. It mirrors internal/ingest/metrics.go: every label
// combination is pre-initialized so Gather() returns the families at zero, and a
// nil registry makes registration a no-op. AnalyticsWorker always constructs it
// (see NewAnalyticsWorker), so call sites never need a nil check.
type AnalyticsMetrics struct {
	runs       *prometheus.CounterVec
	duration   prometheus.Histogram
	inFlight   prometheus.Gauge
	dirtyRepos prometheus.Gauge
}

// NewAnalyticsMetrics builds the analytics worker instruments. reg may be nil,
// in which case nothing is registered but the struct is still usable.
func NewAnalyticsMetrics(reg prometheus.Registerer) *AnalyticsMetrics {
	m := &AnalyticsMetrics{
		runs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "code_graph_analytics_runs_total",
			Help: "Count of analytics recomputes by the worker, by result.",
		}, []string{"result"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "code_graph_analytics_duration_seconds",
			Help:    "Wall time of a single analytics recompute, per repo.",
			Buckets: prometheus.DefBuckets,
		}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "code_graph_analytics_in_flight",
			Help: "Number of analytics recomputes currently running.",
		}),
		dirtyRepos: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "code_graph_analytics_dirty_repos",
			Help: "Number of dirty, settled repos awaiting analytics recompute, set each tick.",
		}),
	}
	if reg != nil {
		reg.MustRegister(m.runs, m.duration, m.inFlight, m.dirtyRepos)
	}
	for _, result := range []string{analyticsResultSuccess, analyticsResultError} {
		m.runs.WithLabelValues(result)
	}
	return m
}

func (m *AnalyticsMetrics) incRun(result string)        { m.runs.WithLabelValues(result).Inc() }
func (m *AnalyticsMetrics) observeDuration(sec float64) { m.duration.Observe(sec) }
func (m *AnalyticsMetrics) incInFlight()                { m.inFlight.Inc() }
func (m *AnalyticsMetrics) decInFlight()                { m.inFlight.Dec() }
func (m *AnalyticsMetrics) setDirtyRepos(n int)         { m.dirtyRepos.Set(float64(n)) }
