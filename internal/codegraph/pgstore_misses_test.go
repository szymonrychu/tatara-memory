//go:build integration

package codegraph_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

func TestSemanticMisses_AbsentDifferentAndMatching(t *testing.T) {
	s, _, ctx := freshStoreWithDB(t)

	// Seed cache: a.go -> sha-a (match), b.go -> sha-old (will differ).
	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:      "sm",
		Extractor: codegraph.ExtractorSemantic,
		Files:     []string{"a.go", "b.go"},
		FileSHAs:  map[string]string{"a.go": "sha-a", "b.go": "sha-old"},
	})
	require.NoError(t, err)

	misses, err := s.SemanticMisses(ctx, "sm", []codegraph.FileSHA{
		{Path: "a.go", ContentSHA: "sha-a"},   // hit (matches)
		{Path: "b.go", ContentSHA: "sha-new"}, // miss (differs)
		{Path: "c.go", ContentSHA: "sha-c"},   // miss (absent)
	})
	require.NoError(t, err)
	sort.Strings(misses)
	require.Equal(t, []string{"b.go", "c.go"}, misses)
}

func TestSemanticMisses_EmptyInput(t *testing.T) {
	s, _, ctx := freshStoreWithDB(t)
	misses, err := s.SemanticMisses(ctx, "sm2", nil)
	require.NoError(t, err)
	require.Empty(t, misses)
}
