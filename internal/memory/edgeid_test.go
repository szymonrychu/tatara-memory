package memory_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestEdgeID_RoundTrip(t *testing.T) {
	cases := []struct {
		from, to string
	}{
		{"a", "b"},
		{"node-1", "node-2"},
		{"with space", "with/slash"},
		{"unicode-α", "β-unicode"},
		{"", "anything"},
		{"only-from", ""},
	}
	for _, c := range cases {
		id := memory.EncodeEdgeID(c.from, c.to)
		from2, to2, err := memory.DecodeEdgeID(id)
		require.NoError(t, err, "decode %q", id)
		require.Equal(t, c.from, from2, "from mismatch for input %q->%q (id=%q)", c.from, c.to, id)
		require.Equal(t, c.to, to2, "to mismatch for input %q->%q (id=%q)", c.from, c.to, id)
	}
}

func TestEdgeID_InvalidPayload(t *testing.T) {
	_, _, err := memory.DecodeEdgeID("not-base64!!")
	require.Error(t, err, "expected error on invalid base64")

	// Valid base64 but no NUL separator: "abcd" -> base64 "YWJjZA" raw url
	_, _, err = memory.DecodeEdgeID("YWJjZA")
	require.Error(t, err, "expected error on missing separator")
}

func TestEdgeID_Determinism(t *testing.T) {
	// Same inputs always produce the same ID.
	require.Equal(t,
		memory.EncodeEdgeID("foo", "bar"),
		memory.EncodeEdgeID("foo", "bar"),
	)
	// Different inputs produce different IDs.
	require.NotEqual(t,
		memory.EncodeEdgeID("foo", "bar"),
		memory.EncodeEdgeID("bar", "foo"),
	)
}
