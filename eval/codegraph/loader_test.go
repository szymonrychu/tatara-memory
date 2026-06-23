package cgeval

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
}

func TestLoadGolden_EmbeddedIsValid(t *testing.T) {
	cases, err := LoadGolden()
	require.NoError(t, err)
	require.NotEmpty(t, cases)

	// Every traversal kind the harness claims to cover must have a case.
	kinds := map[string]bool{}
	for _, c := range cases {
		kinds[c.Kind] = true
	}
	for _, k := range []string{
		KindSearch, KindEntity, KindNeighbors, KindCallers, KindCallees,
		KindDependents, KindDependencies, KindResourceGraph, KindFileImports, KindPath,
	} {
		require.True(t, kinds[k], "missing golden case for kind %q", k)
	}
}

func TestLoadSeed_EmbeddedIsValid(t *testing.T) {
	push, err := LoadSeed()
	require.NoError(t, err)
	require.NotEmpty(t, push.Files)
	require.NotEmpty(t, push.Entities)
	require.NotEmpty(t, push.Edges)
	require.Empty(t, push.Repo, "repo is set by the runner, not the fixture")

	// The fixture must span the discriminative relation classes.
	rels := map[string]bool{}
	for _, e := range push.Edges {
		rels[e.Relation] = true
	}
	for _, want := range []string{"calls", "defines", "imports", "depends_on", "value_ref"} {
		require.True(t, rels[want], "fixture missing %q edges", want)
	}
}

func TestGoldenCase_Mode(t *testing.T) {
	require.Equal(t, ModeRanked, GoldenCase{Kind: KindSearch}.Mode())
	require.Equal(t, ModeRanked, GoldenCase{Kind: KindPath}.Mode())
	require.Equal(t, ModeSet, GoldenCase{Kind: KindCallers}.Mode())
	require.Equal(t, ModeSet, GoldenCase{Kind: KindFileImports}.Mode())
}

func TestGoldenCase_Validate(t *testing.T) {
	ok := GoldenCase{Name: "n", Kind: KindCallers, ID: "x", Expected: []string{"y"}}
	require.NoError(t, ok.validate())

	require.Error(t, GoldenCase{Kind: KindCallers, ID: "x", Expected: []string{"y"}}.validate(), "missing name")
	require.Error(t, GoldenCase{Name: "n", Kind: "bogus", Expected: []string{"y"}}.validate(), "bad kind")
	require.Error(t, GoldenCase{Name: "n", Kind: KindCallers, Expected: []string{"y"}}.validate(), "missing id")
	require.Error(t, GoldenCase{Name: "n", Kind: KindSearch, Expected: []string{"y"}}.validate(), "missing q")
	require.Error(t, GoldenCase{Name: "n", Kind: KindNeighbors, ID: "x", Expected: []string{"y"}}.validate(), "missing relation")
	require.Error(t, GoldenCase{Name: "n", Kind: KindFileImports, Expected: []string{"y"}}.validate(), "missing path")
	require.Error(t, GoldenCase{Name: "n", Kind: KindPath, From: "a", Expected: []string{"y"}}.validate(), "missing to")
	require.Error(t, GoldenCase{Name: "n", Kind: KindCallers, ID: "x"}.validate(), "missing expected")
	require.Error(t, GoldenCase{Name: "n", Kind: KindNeighbors, ID: "x", Relation: "calls", Direction: "sideways", Expected: []string{"y"}}.validate(), "bad direction")
}

func TestParseGolden_RejectsDuplicateNames(t *testing.T) {
	_, err := parseGolden([]byte(`[
		{"name":"dup","kind":"callers","id":"x","expected":["y"]},
		{"name":"dup","kind":"callees","id":"x","expected":["y"]}
	]`))
	require.Error(t, err)
}

func TestLoadSeedDir_RejectsOutOfScopeEdge(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bad.json", `{
		"files": ["a.go"],
		"entities": [{"id":"go:func:a.F","name":"F","type":"function","file_path":"a.go"}],
		"edges": [{"from":"go:func:a.F","to":"go:func:b.G","relation":"calls","src_file":"b.go"}]
	}`)
	_, err := LoadSeedDir(dir)
	require.Error(t, err, "edge src_file not in files")
}
