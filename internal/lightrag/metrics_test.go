package lightrag_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

func counterFor(t *testing.T, mfs []*dto.MetricFamily, op, result string) float64 {
	t.Helper()
	for _, mf := range mfs {
		if mf.GetName() != "lightrag_calls_total" {
			continue
		}
		for _, m := range mf.Metric {
			var opMatch, resultMatch bool
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "op" && lp.GetValue() == op {
					opMatch = true
				}
				if lp.GetName() == "result" && lp.GetValue() == result {
					resultMatch = true
				}
			}
			if opMatch && resultMatch {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func durationCountFor(t *testing.T, mfs []*dto.MetricFamily, op string) uint64 {
	t.Helper()
	for _, mf := range mfs {
		if mf.GetName() != "lightrag_call_duration_seconds" {
			continue
		}
		for _, m := range mf.Metric {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "op" && lp.GetValue() == op {
					return m.GetHistogram().GetSampleCount()
				}
			}
		}
	}
	return 0
}

func TestNewHTTPClient_RegistersMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{
		BaseURL:    "http://lightrag.local",
		HTTPClient: http.DefaultClient,
		Registry:   reg,
	})
	require.NoError(t, err)
	require.NotNil(t, c)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	require.True(t, names["lightrag_calls_total"])
	require.True(t, names["lightrag_call_duration_seconds"])
}

func TestNewHTTPClient_RequiresBaseURL(t *testing.T) {
	_, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{})
	require.Error(t, err)
}

func TestHTTPClient_MetricsIncrement(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	t.Cleanup(srv.Close)

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: srv.URL, Registry: reg})
	require.NoError(t, err)

	_, _ = c.TrackStatus(context.Background(), "track-1")

	mfs, err := reg.Gather()
	require.NoError(t, err)

	require.InDelta(t, 1.0, counterFor(t, mfs, "track_status", "error"), 0.0001)
	require.InDelta(t, 0.0, counterFor(t, mfs, "track_status", "success"), 0.0001)
	require.Equal(t, uint64(1), durationCountFor(t, mfs, "track_status"))
}

func TestHTTPClient_InvalidMode_IncrementsErrorCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be called for invalid mode")
	}))
	t.Cleanup(srv.Close)

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: srv.URL, Registry: reg})
	require.NoError(t, err)

	_, err = c.Query(context.Background(), lightrag.QueryRequest{Query: "x", Mode: "bogus"})
	require.Error(t, err)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	require.InDelta(t, 1.0, counterFor(t, mfs, "query", "error"), 0.0001)
}

func TestHTTPClient_QueryDataInvalidMode_IncrementsErrorCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be called for invalid mode")
	}))
	t.Cleanup(srv.Close)

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: srv.URL, Registry: reg})
	require.NoError(t, err)

	_, err = c.QueryData(context.Background(), lightrag.QueryRequest{Query: "x", Mode: "bad"})
	require.Error(t, err)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	require.InDelta(t, 1.0, counterFor(t, mfs, "query_data", "error"), 0.0001)
}

// TestHealth_LoggedAtDebug verifies that a successful Health() call logs at
// DEBUG (not INFO) so readiness probe noise does not drown business logs
// (finding 10).
func TestHealth_LoggedAtDebug(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: srv.URL, Logger: logger})
	require.NoError(t, err)

	require.NoError(t, c.Health(context.Background()))

	var logLine map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &logLine))
	require.Equal(t, "DEBUG", logLine["level"], "successful Health() must log at DEBUG, not INFO")
}

// Finding 2: QueryData logical failure (HTTP 200 + non-success status) must
// increment the error counter, not just the success counter recorded by do().
func TestHTTPClient_QueryData_LogicalFailure_IncrementsErrorCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// HTTP 200 but status=failure in the envelope.
		_ = json.NewEncoder(w).Encode(lightrag.QueryDataResponse{
			Status:  "failure",
			Message: "upstream error",
		})
	}))
	t.Cleanup(srv.Close)

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: srv.URL, Registry: reg})
	require.NoError(t, err)

	_, err = c.QueryData(context.Background(), lightrag.QueryRequest{Query: "x"})
	require.Error(t, err)
	var le *lightrag.LogicalError
	require.ErrorAs(t, err, &le, "must be a LogicalError")

	mfs, err := reg.Gather()
	require.NoError(t, err)

	// The do() call records success (HTTP 200); the logical-failure branch must
	// then add an additional error count so callers see failure in the metric.
	errCount := counterFor(t, mfs, "query_data", "error")
	require.GreaterOrEqual(t, errCount, 1.0,
		"logical failure must increment query_data error counter")
}

// TestHTTPClient_QueryData_Success_NoExtraErrorCounter verifies the success
// path does NOT spuriously increment the error counter (regression guard).
func TestHTTPClient_QueryData_Success_NoExtraErrorCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(lightrag.QueryDataResponse{
			Status: "success",
			Data:   map[string]any{},
		})
	}))
	t.Cleanup(srv.Close)

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: srv.URL, Registry: reg})
	require.NoError(t, err)

	_, err = c.QueryData(context.Background(), lightrag.QueryRequest{Query: "x"})
	require.NoError(t, err)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	errCount := counterFor(t, mfs, "query_data", "error")
	require.InDelta(t, 0.0, errCount, 0.001, "success path must not increment error counter")
}

// TestHTTPClient_QueryData_Success_NoErrorAttr verifies that a successful
// call does not include an "error" attribute in the log line (finding 10).
func TestHealth_Success_NoErrorAttr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: srv.URL, Logger: logger})
	require.NoError(t, err)

	require.NoError(t, c.Health(context.Background()))

	var logLine map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &logLine))
	_, hasError := logLine["error"]
	require.False(t, hasError, "successful call must not include an 'error' attribute in the log line")
}
