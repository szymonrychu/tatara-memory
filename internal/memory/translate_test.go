package memory_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestToLightragInsert(t *testing.T) {
	m := memory.Memory{ID: "m1", Text: "hello", Metadata: map[string]string{"src": "a"}}
	req := memory.ToLightragInsert(m)
	require.Len(t, req.Documents, 1)
	require.Equal(t, "m1", req.Documents[0].ID)
	require.Equal(t, "hello", req.Documents[0].Content)
	require.Equal(t, "a", req.Documents[0].Metadata["src"])
}

func TestFromLightragQuery(t *testing.T) {
	resp := lightrag.QueryResponse{
		Matches: []lightrag.Match{
			{ID: "m1", Score: 0.9, Text: "hi"},
			{ID: "m2", Score: 0.5, Text: "ho"},
		},
	}
	got := memory.FromLightragQuery(resp)
	require.Len(t, got.Matches, 2)
	require.Equal(t, "m1", got.Matches[0].ID)
	require.InDelta(t, 0.9, got.Matches[0].Score, 1e-6)
}
