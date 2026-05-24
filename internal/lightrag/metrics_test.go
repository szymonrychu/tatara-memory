package lightrag_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

// counterFor extracts the value of lightrag_calls_total{op, result} from a gathered set.
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

// durationCountFor extracts the histogram sample count for lightrag_call_duration_seconds{op}.
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

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{
		BaseURL:  srv.URL,
		Registry: reg,
	})
	require.NoError(t, err)

	_, _ = c.GetDocument(context.Background(), "doc-1")

	mfs, err := reg.Gather()
	require.NoError(t, err)

	// Assert the specific label combination {op=get_document, result=error}.
	require.InDelta(t, 1.0, counterFor(t, mfs, "get_document", "error"), 0.0001)
	// Success counter for the same op must remain zero.
	require.InDelta(t, 0.0, counterFor(t, mfs, "get_document", "success"), 0.0001)
	// Duration histogram records one observation.
	require.Equal(t, uint64(1), durationCountFor(t, mfs, "get_document"))
}

func TestHTTPClient_InvalidMode_IncrementsErrorCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be called for invalid mode")
	}))
	t.Cleanup(srv.Close)

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{
		BaseURL:  srv.URL,
		Registry: reg,
	})
	require.NoError(t, err)

	_, err = c.Query(context.Background(), lightrag.QueryRequest{Query: "x", Mode: "bogus"})
	require.Error(t, err)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	require.InDelta(t, 1.0, counterFor(t, mfs, "query", "error"), 0.0001,
		"lightrag_calls_total{op=query,result=error} must be 1 for invalid mode")
}

func TestHTTPClient_InvalidModeDescribe_IncrementsErrorCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be called for invalid mode")
	}))
	t.Cleanup(srv.Close)

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{
		BaseURL:  srv.URL,
		Registry: reg,
	})
	require.NoError(t, err)

	_, err = c.QueryDescribe(context.Background(), lightrag.QueryRequest{Query: "x", Mode: "bad"})
	require.Error(t, err)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	require.InDelta(t, 1.0, counterFor(t, mfs, "query_describe", "error"), 0.0001,
		"lightrag_calls_total{op=query_describe,result=error} must be 1 for invalid mode")
}
