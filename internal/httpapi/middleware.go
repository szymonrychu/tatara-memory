package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/szymonrychu/tatara-memory/internal/ctxkeys"
)

// requestIDRe accepts only safe characters for X-Request-Id.
var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// requestIDCounter is used as a fallback ID source when crypto/rand fails.
var requestIDCounter atomic.Uint64

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

// RequestIDFromContext retrieves the request ID from the context.
// The key is defined in internal/ctxkeys so that downstream packages
// (e.g. lightrag HTTPClient) can read the same value for log correlation.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxkeys.RequestID).(string)
	return v
}

// RequestID is a middleware that assigns a unique request ID to each request.
// If the incoming request carries an X-Request-Id header that passes validation
// (max 64 chars, only [A-Za-z0-9._-]), that value is used; otherwise a fresh
// ID is generated.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" || len(id) > 64 || !requestIDRe.MatchString(id) {
			id = generateRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), ctxkeys.RequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// generateRequestID returns a hex-encoded random ID, falling back to a
// monotonic counter if crypto/rand fails (finding 4: never emit all-zero ID).
func generateRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%016x", requestIDCounter.Add(1))
	}
	return hex.EncodeToString(b[:])
}

// WithLogger stores logger in the request context so that helper functions
// (e.g. mapServiceError) can emit structured logs without needing cfg access.
func WithLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), ctxkeys.Logger, logger)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// loggerFromContext retrieves the logger stored by WithLogger, falling back
// to slog.Default() when the middleware was not in the chain (e.g. tests that
// construct a plain http.Request).
func loggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxkeys.Logger).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(c int) {
	if s.wroteHeader {
		return
	}
	s.wroteHeader = true
	s.status = c
	s.ResponseWriter.WriteHeader(c)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(b)
}

// Recover is a middleware that catches panics and returns a 500 JSON error envelope.
// For panic logging and metrics use RecoverWithLogger.
func Recover(next http.Handler) http.Handler {
	return RecoverWithLogger(nil, nil)(next)
}

// RecoverWithLogger is Recover with structured ERROR logging and a panic counter.
// logger and panicCounter may be nil (degrades to Recover behaviour).
// If headers were already written before the panic, the 500 envelope is skipped
// (writing it would produce a corrupt/split response); the panic is still logged
// and counted (finding 2).
func RecoverWithLogger(logger *slog.Logger, panicCounter prometheus.Counter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
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
					// Only write the error envelope if headers have not been committed yet.
					if !sr.wroteHeader {
						WriteError(sr, http.StatusInternalServerError, "internal error", RequestIDFromContext(r.Context()))
					}
				}
			}()
			next.ServeHTTP(sr, r)
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
