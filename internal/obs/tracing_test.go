package obs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/szymonrychu/tatara-memory/internal/obs"
)

func TestTracerProvider_NoopWhenEmpty(t *testing.T) {
	tp, shutdown, err := obs.TracerProvider(context.Background(), "", "tatara-memory")
	require.NoError(t, err)
	require.NotNil(t, tp)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	_, ok := tp.(noop.TracerProvider)
	require.True(t, ok, "expected noop tracer provider when endpoint is empty")
}

func TestTracerProvider_OTLPWhenEndpointSet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	// otlptracehttp expects host:port without scheme
	endpoint := srv.Listener.Addr().String()

	ctx := context.Background()
	tp, shutdown, err := obs.TracerProvider(ctx, endpoint, "tatara-memory")
	require.NoError(t, err)
	require.NotNil(t, tp)

	_, isSdk := tp.(*sdktrace.TracerProvider)
	require.True(t, isSdk, "expected *sdktrace.TracerProvider when endpoint is set")

	_, isNoop := tp.(noop.TracerProvider)
	require.False(t, isNoop, "expected real provider when endpoint is set")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, shutdown(shutdownCtx))
}
