package lightrag_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

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

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{
		BaseURL:  srv.URL,
		Registry: reg,
	})
	require.NoError(t, err)

	_, _ = c.Query(context.Background(), lightrag.QueryRequest{
		Query: "x", Mode: lightrag.QueryModeHybrid,
	})

	mfs, err := reg.Gather()
	require.NoError(t, err)

	var calls, dur float64
	for _, mf := range mfs {
		switch mf.GetName() {
		case "lightrag_calls_total":
			for _, m := range mf.Metric {
				calls += m.GetCounter().GetValue()
			}
		case "lightrag_call_duration_seconds":
			for _, m := range mf.Metric {
				dur += float64(m.GetHistogram().GetSampleCount())
			}
		}
	}
	require.InDelta(t, 1, calls, 0.0001)
	require.InDelta(t, 1, dur, 0.0001)
}
