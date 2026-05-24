package obs

import (
	"context"
	"io"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
)

func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(h)
}

type RequestFields struct {
	RequestID  string
	User       string
	Route      string
	Method     string
	Status     int
	DurationMs int64
}

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

type Config struct {
	LogWriter    io.Writer
	LogLevel     slog.Level
	ServiceName  string
	OTLPEndpoint string
}

type Obs struct {
	Logger   *slog.Logger
	Registry *prometheus.Registry
	Tracer   trace.TracerProvider
	Shutdown ShutdownFunc
}

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
