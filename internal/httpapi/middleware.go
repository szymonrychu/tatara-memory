package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
)

// routeLabel returns the matched chi route pattern (e.g. "/memories/{id}") for
// use as a bounded metric/log label. Unmatched requests (404s, or calls made
// outside a chi router) collapse to a single "unmatched" value so arbitrary
// paths cannot inflate label cardinality.
func routeLabel(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			return p
		}
	}
	return "unmatched"
}

type ctxKey int

const requestIDKey ctxKey = 0

// RequestIDFromContext retrieves the request ID from the context.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

// RequestID is a middleware that assigns a unique request ID to each request.
// If the incoming request carries an X-Request-Id header, that value is used.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			var b [8]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(c int) {
	s.status = c
	s.ResponseWriter.WriteHeader(c)
}

// Recover is a middleware that catches panics and returns a 500 JSON error envelope.
// For panic logging and metrics use RecoverWithLogger.
func Recover(next http.Handler) http.Handler {
	return RecoverWithLogger(nil, nil)(next)
}

// RecoverWithLogger is Recover with structured ERROR logging and a panic counter.
// logger and panicCounter may be nil (degrades to Recover behaviour).
func RecoverWithLogger(logger *slog.Logger, panicCounter prometheus.Counter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if logger != nil {
						logger.ErrorContext(r.Context(), "http panic recovered",
							"request_id", RequestIDFromContext(r.Context()),
							"panic", rec,
							"stack", string(debug.Stack()),
						)
					}
					if panicCounter != nil {
						panicCounter.Inc()
					}
					WriteError(w, http.StatusInternalServerError, "internal error", RequestIDFromContext(r.Context()))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// AccessLog returns a middleware that logs each request using the given slog.Logger.
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(rec, r)
			logger.Info("http",
				"request_id", RequestIDFromContext(r.Context()),
				"route", routeLabel(r),
				"path", r.URL.Path,
				"method", r.Method,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// Metrics tracks HTTP request counts, durations, in-flight counts, and panics via Prometheus.
type Metrics struct {
	reqTotal   *prometheus.CounterVec
	reqDur     *prometheus.HistogramVec
	inFlight   prometheus.Gauge
	panicTotal prometheus.Counter
}

// NewMetrics creates and registers HTTP metrics with the given Prometheus registerer.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		reqTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Count of HTTP requests by route, method, status.",
		}, []string{"route", "method", "status"}),
		reqDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_in_flight",
			Help: "In-flight HTTP requests.",
		}),
		panicTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "http_panics_total",
			Help: "Count of HTTP handler panics recovered by the Recover middleware.",
		}),
	}
	reg.MustRegister(m.reqTotal, m.reqDur, m.inFlight, m.panicTotal)
	return m
}

// PanicCounter returns the panic counter for wiring into RecoverWithLogger.
func (m *Metrics) PanicCounter() prometheus.Counter { return m.panicTotal }

// Middleware returns an http.Handler middleware that records request metrics.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.inFlight.Inc()
		defer m.inFlight.Dec()
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		route := routeLabel(r)
		m.reqTotal.WithLabelValues(route, r.Method, http.StatusText(rec.status)).Inc()
		m.reqDur.WithLabelValues(route).Observe(time.Since(start).Seconds())
	})
}
