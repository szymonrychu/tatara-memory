package ingest

import "github.com/prometheus/client_golang/prometheus"

// Item result classes for ingest_items_total.
const (
	resultSuccess = "success"
	resultError   = "error"
	resultTimeout = "timeout"
)

// Job status classes for ingest_jobs_total. These mirror the terminal
// memory.JobStatus values used by runJob's finalization switch.
const (
	jobSucceeded = "succeeded"
	jobFailed    = "failed"
	jobPartial   = "partial"
)

// metrics holds the Prometheus instruments for the async ingest worker pool.
// It mirrors internal/lightrag/metrics.go: a struct built by newMetrics, with
// every label combination pre-initialized so Gather() returns the families at
// zero, and a nil registry making registration a no-op. The struct is always
// constructed (see newPool), so call sites never need a nil check.
type metrics struct {
	items            *prometheus.CounterVec
	itemDuration     prometheus.Histogram
	jobs             *prometheus.CounterVec
	inFlight         prometheus.Gauge
	notifyDropped    prometheus.Counter
	sourceIndexError prometheus.Counter
	storeOpError     prometheus.Counter
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		items: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ingest_items_total",
			Help: "Count of ingest items processed by the worker pool, by result.",
		}, []string{"result"}),
		itemDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ingest_item_duration_seconds",
			Help:    "Duration of processing a single ingest item.",
			Buckets: prometheus.DefBuckets,
		}),
		jobs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ingest_jobs_total",
			Help: "Count of ingest jobs finalized by the worker pool, by terminal status.",
		}, []string{"status"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ingest_items_in_flight",
			Help: "Number of ingest items currently being processed by the worker pool.",
		}),
		notifyDropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ingest_notify_dropped_total",
			Help: "Count of job IDs dropped by Notify because the notify channel was full.",
		}),
		sourceIndexError: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ingest_source_index_errors_total",
			Help: "Count of non-fatal source-index (SourceSink.Add) failures after a successful CreateMemory.",
		}),
		storeOpError: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ingest_store_op_errors_total",
			Help: "Count of JobStore operation failures in runJob (progress, finalize). Silent loss is observable.",
		}),
	}
	if reg != nil {
		reg.MustRegister(
			m.items, m.itemDuration, m.jobs, m.inFlight,
			m.notifyDropped, m.sourceIndexError, m.storeOpError,
		)
	}
	for _, result := range []string{resultSuccess, resultError, resultTimeout} {
		m.items.WithLabelValues(result)
	}
	for _, status := range []string{jobSucceeded, jobFailed, jobPartial} {
		m.jobs.WithLabelValues(status)
	}
	return m
}

// observeItem records the duration and result of a single processed item.
// timeout is distinguished from a generic error by the caller via
// errors.Is(err, context.DeadlineExceeded).
func (m *metrics) observeItem(dur float64, result string) {
	m.items.WithLabelValues(result).Inc()
	m.itemDuration.Observe(dur)
}

func (m *metrics) incJob(status string) {
	m.jobs.WithLabelValues(status).Inc()
}

func (m *metrics) incInFlight() {
	m.inFlight.Inc()
}

func (m *metrics) decInFlight() {
	m.inFlight.Dec()
}

func (m *metrics) incNotifyDropped() {
	m.notifyDropped.Inc()
}

func (m *metrics) incSourceIndexError() {
	m.sourceIndexError.Inc()
}

func (m *metrics) incStoreOpError() {
	m.storeOpError.Inc()
}
