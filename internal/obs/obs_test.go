package obs_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/obs"
)

func TestNew_AssemblesAllThree(t *testing.T) {
	var buf bytes.Buffer
	o, err := obs.New(context.Background(), obs.Config{
		LogWriter:    &buf,
		LogLevel:     slog.LevelInfo,
		ServiceName:  "tatara-memory",
		OTLPEndpoint: "",
	})
	require.NoError(t, err)
	require.NotNil(t, o.Logger)
	require.NotNil(t, o.Registry)
	require.NotNil(t, o.Tracer)
	require.NotNil(t, o.Shutdown)
}
