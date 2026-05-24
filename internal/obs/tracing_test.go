package obs_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
	ctx := context.Background()
	tp, shutdown, err := obs.TracerProvider(ctx, "localhost:4317", "tatara-memory")
	require.NoError(t, err)
	require.NotNil(t, tp)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = shutdown(ctx)
	})

	_, isNoop := tp.(noop.TracerProvider)
	require.False(t, isNoop, "expected real provider when endpoint is set")
}
