package obs

import (
	"io"
	"log/slog"
)

// NewLogger returns a JSON-format slog.Logger writing to w at the given level.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(h)
}
