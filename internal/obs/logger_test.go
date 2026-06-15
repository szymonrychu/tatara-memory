package obs_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/obs"
)

func TestLogger_EmitsValidJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := obs.NewLogger(&buf, slog.LevelInfo)
	require.NotNil(t, logger)

	logger.Info("hello", slog.String("request_id", "abc"))

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "hello", got["msg"])
	require.Equal(t, "abc", got["request_id"])
	require.Equal(t, "INFO", got["level"])
	require.Contains(t, got, "time")
}
