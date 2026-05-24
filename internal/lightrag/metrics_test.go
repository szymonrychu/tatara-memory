package lightrag_test

import (
	"net/http"
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
