package eval

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestLoadGolden_ShippedCasesValidate(t *testing.T) {
	cases, err := LoadGolden()
	require.NoError(t, err)
	require.NotEmpty(t, cases, "shipped golden set must be non-empty")
	for _, c := range cases {
		require.NoError(t, c.validate(), "shipped case %q must validate", c.Name)
		require.True(t, c.Mode.Valid(), "shipped case %q has invalid mode", c.Name)
		require.GreaterOrEqual(t, c.TopK, 1, "shipped case %q top_k normalized", c.Name)
		require.LessOrEqual(t, c.TopK, maxTopK)
	}
}

func TestLoadGolden_UniqueNames(t *testing.T) {
	cases, err := LoadGolden()
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, c := range cases {
		require.False(t, seen[c.Name], "duplicate golden name %q", c.Name)
		seen[c.Name] = true
	}
}

func TestLoadSeed_ShippedCorpusValidates(t *testing.T) {
	items, err := LoadSeed()
	require.NoError(t, err)
	require.NotEmpty(t, items, "shipped seed corpus must be non-empty")
	seen := map[string]bool{}
	for _, it := range items {
		require.NotEmpty(t, it.IdempotencyKey, "seed item missing idempotency_key")
		require.NotEmpty(t, it.Text, "seed item %q missing text", it.IdempotencyKey)
		require.False(t, seen[it.IdempotencyKey], "duplicate seed key %q", it.IdempotencyKey)
		seen[it.IdempotencyKey] = true
	}
}

func TestParseGolden_Valid(t *testing.T) {
	data := []byte(`[{"name":"a","query":"q","mode":"hybrid","top_k":5,"expected":["x"]}]`)
	cases, err := parseGolden(data)
	require.NoError(t, err)
	require.Len(t, cases, 1)
	require.Equal(t, "a", cases[0].Name)
	require.Equal(t, memory.QueryModeHybrid, cases[0].Mode)
	require.Equal(t, 5, cases[0].TopK)
	require.Equal(t, []string{"x"}, cases[0].Expected)
}

func TestParseGolden_DefaultsAndClampsTopK(t *testing.T) {
	cases, err := parseGolden([]byte(`[
		{"name":"zero","query":"q","mode":"local","expected":["x"]},
		{"name":"over","query":"q","mode":"local","top_k":99999,"expected":["x"]}
	]`))
	require.NoError(t, err)
	require.Equal(t, defaultTopK, cases[0].TopK, "omitted top_k defaults to 10")
	require.Equal(t, maxTopK, cases[1].TopK, "top_k clamped to 500")
}

func TestParseGolden_Rejects(t *testing.T) {
	bad := map[string]string{
		"empty query":     `[{"name":"a","query":"","mode":"hybrid","expected":["x"]}]`,
		"missing name":    `[{"name":"","query":"q","mode":"hybrid","expected":["x"]}]`,
		"invalid mode":    `[{"name":"a","query":"q","mode":"semantic","expected":["x"]}]`,
		"empty expected":  `[{"name":"a","query":"q","mode":"hybrid","expected":[]}]`,
		"blank expected":  `[{"name":"a","query":"q","mode":"hybrid","expected":["  "]}]`,
		"negative top_k":  `[{"name":"a","query":"q","mode":"hybrid","top_k":-1,"expected":["x"]}]`,
		"duplicate names": `[{"name":"a","query":"q","mode":"hybrid","expected":["x"]},{"name":"a","query":"q2","mode":"local","expected":["y"]}]`,
		"malformed json":  `{not json}`,
		"unknown field":   `[{"name":"a","query":"q","mode":"hybrid","expected":["x"],"bogus":1}]`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := parseGolden([]byte(body))
			require.Error(t, err, "expected %s to be rejected", name)
		})
	}
}

func TestParseSeed_Rejects(t *testing.T) {
	bad := map[string]string{
		"missing key":   `[{"idempotency_key":"","text":"t"}]`,
		"missing text":  `[{"idempotency_key":"k","text":""}]`,
		"duplicate key": `[{"idempotency_key":"k","text":"a"},{"idempotency_key":"k","text":"b"}]`,
		"malformed":     `{nope}`,
		"unknown field": `[{"idempotency_key":"k","text":"t","bogus":1}]`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := parseSeed([]byte(body))
			require.Error(t, err, "expected %s to be rejected", name)
		})
	}
}

func TestParseSeed_Valid(t *testing.T) {
	items, err := parseSeed([]byte(`[{"idempotency_key":"k","text":"t","metadata":{"a":"b"}}]`))
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "k", items[0].IdempotencyKey)
	require.Equal(t, "b", items[0].Metadata["a"])
}
