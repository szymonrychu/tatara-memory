package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

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
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				WriteError(w, http.StatusInternalServerError, "internal error", RequestIDFromContext(r.Context()))
			}
		}()
		next.ServeHTTP(w, r)
	})
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
				"route", r.URL.Path,
				"method", r.Method,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// Metrics tracks HTTP request counts, durations, and in-flight counts via Prometheus.
type Metrics struct {
	reqTotal *prometheus.CounterVec
	reqDur   *prometheus.HistogramVec
	inFlight prometheus.Gauge
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
	}
	reg.MustRegister(m.reqTotal, m.reqDur, m.inFlight)
	return m
}

// Middleware returns an http.Handler middleware that records request metrics.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.inFlight.Inc()
		defer m.inFlight.Dec()
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		m.reqTotal.WithLabelValues(r.URL.Path, r.Method, http.StatusText(rec.status)).Inc()
		m.reqDur.WithLabelValues(r.URL.Path).Observe(time.Since(start).Seconds())
	})
}
