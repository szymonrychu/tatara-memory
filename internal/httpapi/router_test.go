package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
)

func TestHealthz(t *testing.T) {
	r := httpapi.NewRouter(httpapi.Config{Service: &stubService{}})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestRouterServesMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := httpapi.NewRouter(httpapi.Config{Service: &stubService{}, Registry: reg})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)
}

func TestRouterReadyz(t *testing.T) {
	r := httpapi.NewRouter(httpapi.Config{Service: &stubService{}, ReadyCheck: func(_ context.Context) error { return nil }})
	srv := httptest.NewServer(r)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/readyz")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)
}
