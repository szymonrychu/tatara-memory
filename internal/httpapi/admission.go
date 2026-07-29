package httpapi

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Admission outcomes, used as the `result` metric label.
const (
	admissionAdmitted = "admitted"
	admissionShed     = "shed"
	admissionCanceled = "canceled"
)

// AdmissionMetrics instruments one or more admission-control classes.
// A nil Registerer yields a usable no-op set (same contract as the ingest and
// lightrag metric structs). Every label combination is pre-initialised so
// Gather() reports the families at zero before the first request, which is what
// makes "we are shedding nothing" distinguishable from "this metric does not
// exist yet" on a dashboard.
type AdmissionMetrics struct {
	inFlight *prometheus.GaugeVec
	waiting  *prometheus.GaugeVec
	total    *prometheus.CounterVec
	wait     *prometheus.HistogramVec
}

// NewAdmissionMetrics registers the admission-control metric families with reg
// and pre-initialises the label combinations for the given classes.
func NewAdmissionMetrics(reg prometheus.Registerer, classes ...string) *AdmissionMetrics {
	m := &AdmissionMetrics{
		inFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "http_admission_in_flight",
			Help: "Requests currently holding an admission-control slot, by class.",
		}, []string{"class"}),
		waiting: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "http_admission_waiting",
			Help: "Requests queued for an admission-control slot, by class.",
		}, []string{"class"}),
		total: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_admission_total",
			Help: "Admission-control decisions by class and result (admitted, shed, canceled).",
		}, []string{"class", "result"}),
		wait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_admission_wait_seconds",
			Help:    "Time spent waiting for an admission-control slot, by class and result.",
			Buckets: []float64{0.001, 0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30},
		}, []string{"class", "result"}),
	}
	if reg != nil {
		reg.MustRegister(m.inFlight, m.waiting, m.total, m.wait)
	}
	for _, c := range classes {
		m.inFlight.WithLabelValues(c)
		m.waiting.WithLabelValues(c)
		for _, res := range []string{admissionAdmitted, admissionShed, admissionCanceled} {
			m.total.WithLabelValues(c, res)
			m.wait.WithLabelValues(c, res)
		}
	}
	return m
}

// AdmissionConfig describes one admission-control class: a bounded number of
// concurrently executing requests, plus how long a request may queue for a slot
// before it is shed.
type AdmissionConfig struct {
	// Class names the budget in logs and metrics (e.g. "memories_bulk").
	Class string
	// MaxInFlight bounds concurrently executing requests. 0 disables the
	// limiter entirely (the repo-wide "0 disables" convention).
	MaxInFlight int
	// Wait is how long a request may queue for a slot before being shed.
	// Non-positive sheds immediately when saturated.
	Wait time.Duration
	// RetryAfter is the base value advertised in the Retry-After header of a
	// shed response. Jitter of up to +50% is added per response.
	RetryAfter time.Duration
	// Logger receives one WARN line per shed. nil uses slog.Default().
	Logger *slog.Logger
	// Metrics is required; build it with NewAdmissionMetrics.
	Metrics *AdmissionMetrics
}

// AdmissionLimiter bounds how many requests of one class execute at once.
//
// This is the load-shedding half of the bulk-ingest backpressure fix
// (tatara-memory#79/#80/#82/#87). The service holds a single small Postgres
// connection pool shared by /memories:bulk, /code-graph:bulk, the ingest worker
// pool and every read route; a concurrent-ingest burst used to admit unlimited
// bulk work, so long /code-graph:bulk reconcile transactions could hold every
// connection and starve /memories:bulk's CreateJob until its deadline fired.
// Bounding each bulk class below the pool size guarantees the rest of the
// service keeps connections, and turns "starve, then time out, then report 503"
// into "shed early with 429 + Retry-After".
type AdmissionLimiter struct {
	class      string
	slots      chan struct{}
	wait       time.Duration
	retryAfter time.Duration
	log        *slog.Logger
	metrics    *AdmissionMetrics
	shedSeq    atomic.Uint64
}

// NewAdmissionLimiter returns a limiter for one class. A MaxInFlight of 0
// returns a pass-through limiter.
func NewAdmissionLimiter(cfg AdmissionConfig) *AdmissionLimiter {
	l := &AdmissionLimiter{
		class:      cfg.Class,
		wait:       cfg.Wait,
		retryAfter: cfg.RetryAfter,
		log:        cfg.Logger,
		metrics:    cfg.Metrics,
	}
	if l.log == nil {
		l.log = slog.Default()
	}
	if l.metrics == nil {
		l.metrics = NewAdmissionMetrics(nil, cfg.Class)
	}
	if cfg.MaxInFlight > 0 {
		l.slots = make(chan struct{}, cfg.MaxInFlight)
	}
	return l
}

// Middleware bounds concurrent execution of next. A request that cannot get a
// slot within the wait budget is shed with 429 + Retry-After; a request whose
// client disconnects while queued gets the existing 499 treatment.
func (l *AdmissionLimiter) Middleware(next http.Handler) http.Handler {
	if l == nil || l.slots == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		select {
		case l.slots <- struct{}{}:
			l.observe(admissionAdmitted, 0)
		default:
			if !l.queue(w, r, start) {
				return
			}
		}
		defer func() { <-l.slots }()

		l.metrics.inFlight.WithLabelValues(l.class).Inc()
		defer l.metrics.inFlight.WithLabelValues(l.class).Dec()
		next.ServeHTTP(w, r)
	})
}

// queue waits for a slot on the saturated path. It reports whether the request
// was admitted; when it returns false the response has already been written.
func (l *AdmissionLimiter) queue(w http.ResponseWriter, r *http.Request, start time.Time) bool {
	l.metrics.waiting.WithLabelValues(l.class).Inc()
	defer l.metrics.waiting.WithLabelValues(l.class).Dec()

	timer := time.NewTimer(max(l.wait, 0))
	defer timer.Stop()

	select {
	case l.slots <- struct{}{}:
		l.observe(admissionAdmitted, time.Since(start))
		return true
	case <-timer.C:
		l.shed(w, r, time.Since(start))
		return false
	case <-r.Context().Done():
		l.observe(admissionCanceled, time.Since(start))
		WriteError(w, 499, "client closed request", RequestIDFromContext(r.Context()))
		return false
	}
}

// shed rejects a request that could not be admitted within the wait budget.
func (l *AdmissionLimiter) shed(w http.ResponseWriter, r *http.Request, waited time.Duration) {
	l.observe(admissionShed, waited)
	retryAfter := l.retryAfterSeconds()
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	l.log.WarnContext(r.Context(), "http.admission.shed",
		"action", "admission_shed",
		"request_id", RequestIDFromContext(r.Context()),
		"class", l.class,
		"route", routeLabel(r),
		"method", r.Method,
		"waited_ms", waited.Milliseconds(),
		"retry_after_s", retryAfter,
	)
	WriteError(w, http.StatusTooManyRequests,
		"too many concurrent bulk requests, retry after backoff",
		RequestIDFromContext(r.Context()))
}

func (l *AdmissionLimiter) observe(result string, waited time.Duration) {
	l.metrics.total.WithLabelValues(l.class, result).Inc()
	l.metrics.wait.WithLabelValues(l.class, result).Observe(waited.Seconds())
}

// retryAfterEighths spreads Retry-After over base + {0, 1/8, 1/4, 3/8} of
// base, i.e. never more than +50%. Round-robin selection through a fixed table
// keeps the whole calculation in int (a table index may be any integer type),
// so no counter-to-int conversion is needed.
var retryAfterEighths = [4]int{0, 1, 2, 3}

// retryAfterSeconds returns the Retry-After delta-seconds for one shed
// response. The value is spread rather than constant: a counter, not
// math/rand, so it needs no seeding, stays deterministic under test, and keeps
// a weak RNG out of the code path. Without the spread, a mass concurrent
// re-ingest wave (tatara-memory#87) would all be told to come back at the same
// instant and would re-saturate the service in lockstep.
func (l *AdmissionLimiter) retryAfterSeconds() int {
	base := int(math.Ceil(l.retryAfter.Seconds()))
	if base < 1 {
		base = 1
	}
	return base + base*retryAfterEighths[l.shedSeq.Add(1)&3]/8
}
