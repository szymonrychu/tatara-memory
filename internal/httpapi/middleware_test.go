package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
)

func TestRequestIDMiddlewareGenerates(t *testing.T) {
	var seen string
	h := httpapi.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = httpapi.RequestIDFromContext(r.Context())
		w.WriteHeader(200)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/x", nil))
	require.NotEmpty(t, seen)
	require.Equal(t, seen, rr.Header().Get("X-Request-Id"))
}

func TestRequestIDMiddlewarePassthrough(t *testing.T) {
	h := httpapi.RequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Request-Id", "client-supplied")
	h.ServeHTTP(rr, req)
	require.Equal(t, "client-supplied", rr.Header().Get("X-Request-Id"))
}

func TestRecoverMiddlewareReturns500(t *testing.T) {
	h := httpapi.RequestID(httpapi.Recover(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/x", nil))
	require.Equal(t, 500, rr.Code)
	require.Contains(t, rr.Body.String(), "internal error")
}

// TestRecoverWithLogger verifies that RecoverWithLogger logs at ERROR with the panic
// value + stack, and increments the panic counter (finding 4).
func TestRecoverWithLogger_LogsAndCountsPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctr := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_panics_total"})

	h := httpapi.RequestID(httpapi.RecoverWithLogger(logger, ctr)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("test-panic-value")
	})))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/x", nil))

	require.Equal(t, 500, rr.Code)

	// counter must have been incremented
	ch := make(chan prometheus.Metric, 1)
	ctr.Collect(ch)
	m := <-ch
	var pb dto.Metric
	require.NoError(t, m.Write(&pb))
	require.InDelta(t, 1.0, pb.GetCounter().GetValue(), 0.0001, "panic counter not incremented")

	// logger must have emitted an ERROR entry with the panic value
	var logLine map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &logLine))
	require.Equal(t, "ERROR", logLine["level"])
	require.Equal(t, "test-panic-value", logLine["panic"])
	require.NotEmpty(t, logLine["stack"])
}

// TestNewMetrics_PanicCounterRegistered verifies that NewMetrics registers
// http_panics_total so a wedged handler is visible in Prometheus (finding 4).
func TestNewMetrics_PanicCounterRegistered(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = httpapi.NewMetrics(reg)

	mfs, err := reg.Gather()
	require.NoError(t, err)
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	require.True(t, names["http_panics_total"], "http_panics_total not registered")
}

func TestAccessLogMiddlewareCallsNext(t *testing.T) {
	called := false
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := httpapi.AccessLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(204)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/x", nil))
	require.True(t, called)
	require.Equal(t, 204, rr.Code)
}

func TestMetricsMiddlewareCountsRequest(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := httpapi.NewMetrics(reg)
	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/memories", nil))

	mfs, err := reg.Gather()
	require.NoError(t, err)
	found := false
	for _, mf := range mfs {
		if mf.GetName() == "http_requests_total" {
			found = true
		}
	}
	require.True(t, found, "http_requests_total not registered")
}

// TestMetricsUsesRoutePattern verifies that two requests to the same
// parameterised route with different ids collapse to a single time series
// labelled with the chi route pattern, carrying a combined count of 2.
func TestMetricsUsesRoutePattern(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := httpapi.NewRouter(httpapi.Config{Service: &stubService{}, Registry: reg})

	// Drive the router synchronously: ServeHTTP returns only after the whole
	// middleware chain (including the post-handler metric increment) has run,
	// so Gather below observes a settled registry with no client/server race.
	for _, id := range []string{"abc", "def"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/memories/"+id, nil))
	}

	mfs, err := reg.Gather()
	require.NoError(t, err)

	var series int
	var count float64
	for _, mf := range mfs {
		if mf.GetName() != "http_requests_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			route := labelValue(m, "route")
			if route == "/memories/{id}" {
				series++
				count = m.GetCounter().GetValue()
			}
			require.NotContains(t, route, "abc", "raw id leaked into route label")
			require.NotContains(t, route, "def", "raw id leaked into route label")
		}
	}
	require.Equal(t, 1, series, "expected a single /memories/{id} series")
	require.Equal(t, float64(2), count, "expected combined count of 2")
}

// TestMetricsUnmatchedCollapses verifies that requests to non-existent paths
// share a single bounded "unmatched" label value rather than minting a series
// per probed path.
func TestMetricsUnmatchedCollapses(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := httpapi.NewRouter(httpapi.Config{Service: &stubService{}, Registry: reg})

	for _, p := range []string{"/does-not-exist", "/also/missing"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", p, nil))
	}

	mfs, err := reg.Gather()
	require.NoError(t, err)

	var unmatched float64
	for _, mf := range mfs {
		if mf.GetName() != "http_requests_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelValue(m, "route") == "unmatched" {
				unmatched += m.GetCounter().GetValue()
			}
		}
	}
	require.Equal(t, float64(2), unmatched, "expected both 404s under the unmatched label")
}

func labelValue(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

// TestRecoverDoesNotDoubleWriteHeader verifies that if a handler writes a header
// before panicking, the Recover middleware does NOT attempt a second WriteHeader
// (finding 2: superfluous-WriteHeader / corrupt response).
func TestRecoverDoesNotDoubleWriteHeader(t *testing.T) {
	// Handler: write 200 header, then panic mid-body.
	handler := httpapi.RecoverWithLogger(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic("mid-body panic")
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/x", nil))

	// The key assertion: no 500 body was appended after 200 was committed.
	// If WriteError ran unconditionally we'd see "internal error" in the body.
	require.NotContains(t, recorder.Body.String(), "internal error",
		"Recover must not write 500 envelope after headers are already committed")
}

// TestRequestIDValidatesInboundHeader verifies that an X-Request-Id containing
// invalid characters (e.g. newlines) is rejected and a fresh ID is generated
// (finding 3: log injection / response-header reflection).
func TestRequestIDValidatesInboundHeader(t *testing.T) {
	var contextID string
	h := httpapi.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contextID = httpapi.RequestIDFromContext(r.Context())
		w.WriteHeader(200)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Request-Id", "evil\ninjected-header: val")
	h.ServeHTTP(rr, req)

	// The injected value must not appear in the response header.
	require.NotContains(t, rr.Header().Get("X-Request-Id"), "\n",
		"newline must not be reflected into X-Request-Id response header")
	// A fresh ID must have been generated.
	require.NotEqual(t, "evil\ninjected-header: val", contextID,
		"invalid inbound X-Request-Id must be replaced by a generated one")
	require.NotEmpty(t, contextID)
}

// TestRequestIDValidatesLength verifies that an X-Request-Id longer than 64
// chars is rejected and a fresh ID is generated (finding 3).
func TestRequestIDValidatesLength(t *testing.T) {
	var contextID string
	h := httpapi.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contextID = httpapi.RequestIDFromContext(r.Context())
		w.WriteHeader(200)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Request-Id", string(make([]byte, 65)))
	h.ServeHTTP(rr, req)

	longID := rr.Header().Get("X-Request-Id")
	require.LessOrEqual(t, len(longID), 64, "reflected X-Request-Id must not exceed 64 chars")
	require.NotEmpty(t, contextID)
	require.LessOrEqual(t, len(contextID), 64)
}

// TestRequestIDFallsBackOnRandError verifies that a zero rand.Read result does
// not produce an all-zero request ID (finding 4).
// This test exercises the normal path (no way to inject rand failure) but
// verifies that generated IDs are non-empty and non-zero.
func TestRequestIDGeneratesNonZeroID(t *testing.T) {
	var ids []string
	h := httpapi.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids = append(ids, httpapi.RequestIDFromContext(r.Context()))
		w.WriteHeader(200)
	}))

	for i := 0; i < 5; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", "/x", nil))
	}

	for _, id := range ids {
		require.NotEmpty(t, id)
		require.NotEqual(t, "0000000000000000", id, "all-zero ID detected")
	}
}
