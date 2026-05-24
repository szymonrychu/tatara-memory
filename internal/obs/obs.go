package obs

import (
	"io"
	"log/slog"
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
