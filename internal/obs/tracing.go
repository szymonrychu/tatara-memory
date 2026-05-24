package obs

import (
	"context"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// ShutdownFunc is a function that shuts down a tracer provider and flushes any pending spans.
type ShutdownFunc func(context.Context) error

// TracerProvider returns a noop provider when endpoint is empty, or an OTLP HTTP provider otherwise.
func TracerProvider(ctx context.Context, endpoint, serviceName string) (trace.TracerProvider, ShutdownFunc, error) {
	if endpoint == "" {
		return noop.NewTracerProvider(), func(context.Context) error { return nil }, nil
	}
	return newOTLPProvider(ctx, endpoint, serviceName)
}

func newOTLPProvider(ctx context.Context, endpoint, serviceName string) (trace.TracerProvider, ShutdownFunc, error) {
	exp, err := otlptrace.New(ctx, otlptracehttp.NewClient(
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	))
	if err != nil {
		return nil, nil, err
	}
	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	return tp, tp.Shutdown, nil
}
