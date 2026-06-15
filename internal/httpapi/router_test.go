package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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

func TestRouterReadyzLogsErrorOnFailure(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	readyErr := errors.New("db connection refused")
	r := httpapi.NewRouter(httpapi.Config{
		Service: &stubService{},
		Logger:  logger,
		ReadyCheck: func(_ context.Context) error {
			return readyErr
		},
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/readyz")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	// Expect at least one WARN log line containing the error message.
	logged := false
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec["level"] == "WARN" {
			if errVal, ok := rec["error"]; ok && errVal == readyErr.Error() {
				logged = true
				break
			}
		}
	}
	require.True(t, logged, "expected WARN log with error=%q, got: %s", readyErr.Error(), buf.String())
}
