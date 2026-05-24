package obs

import (
	"context"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type ShutdownFunc func(context.Context) error

func TracerProvider(ctx context.Context, endpoint, serviceName string) (trace.TracerProvider, ShutdownFunc, error) {
	if endpoint == "" {
		return noop.NewTracerProvider(), func(context.Context) error { return nil }, nil
	}
	return newOTLPProvider(ctx, endpoint, serviceName)
}

func newOTLPProvider(ctx context.Context, endpoint, serviceName string) (trace.TracerProvider, ShutdownFunc, error) {
	exp, err := otlptrace.New(ctx, otlptracegrpc.NewClient(
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	))
	if err != nil {
		return nil, nil, err
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
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
