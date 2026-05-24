package obs

import (
	"context"
	"io"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
)

// NewLogger returns a JSON-format slog.Logger writing to w at the given level.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(h)
}

// RequestFields holds the structured fields attached to every HTTP request log entry.
type RequestFields struct {
	RequestID  string
	User       string
	Route      string
	Method     string
	Status     int
	DurationMs int64
}

// RequestLogger returns a child logger with standard HTTP request fields pre-attached.
func RequestLogger(base *slog.Logger, f RequestFields) *slog.Logger {
	return base.With(
		slog.String("request_id", f.RequestID),
		slog.String("user", f.User),
		slog.String("route", f.Route),
		slog.String("method", f.Method),
		slog.Int("status", f.Status),
		slog.Int64("duration_ms", f.DurationMs),
	)
}

// Config holds the options for constructing an Obs bundle.
type Config struct {
	LogWriter    io.Writer
	LogLevel     slog.Level
	ServiceName  string
	OTLPEndpoint string
}

// Obs bundles the three observability pillars: structured logging, metrics, and tracing.
type Obs struct {
	Logger   *slog.Logger
	Registry *prometheus.Registry
	Tracer   trace.TracerProvider
	Shutdown ShutdownFunc
}

// New constructs an Obs bundle from cfg, initialising logging, metrics, and tracing.
func New(ctx context.Context, cfg Config) (*Obs, error) {
	tp, shutdown, err := TracerProvider(ctx, cfg.OTLPEndpoint, cfg.ServiceName)
	if err != nil {
		return nil, err
	}
	return &Obs{
		Logger:   NewLogger(cfg.LogWriter, cfg.LogLevel),
		Registry: PromRegistry(),
		Tracer:   tp,
		Shutdown: shutdown,
	}, nil
}
