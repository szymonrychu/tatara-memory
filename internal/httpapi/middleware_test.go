package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
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
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/v1/memories", nil))

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
