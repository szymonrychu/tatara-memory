package memory_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestMemoryZeroValue(t *testing.T) {
	var m memory.Memory
	require.Empty(t, m.ID)
	require.Empty(t, m.Text)
}

func TestQueryModeValid(t *testing.T) {
	cases := []struct {
		mode memory.QueryMode
		ok   bool
	}{
		{memory.QueryModeHybrid, true},
		{memory.QueryModeLocal, true},
		{memory.QueryModeGlobal, true},
		{memory.QueryModeNaive, true},
		{memory.QueryMode("bogus"), false},
	}
	for _, c := range cases {
		require.Equal(t, c.ok, c.mode.Valid(), "mode=%s", c.mode)
	}
}

func TestJobStatusTerminal(t *testing.T) {
	require.True(t, memory.JobStatusSucceeded.Terminal())
	require.True(t, memory.JobStatusFailed.Terminal())
	require.True(t, memory.JobStatusPartial.Terminal())
	require.False(t, memory.JobStatusQueued.Terminal())
	require.False(t, memory.JobStatusRunning.Terminal())
}

func TestIngestJobNow(t *testing.T) {
	j := memory.IngestJob{CreatedAt: time.Now()}
	require.False(t, j.CreatedAt.IsZero())
}
