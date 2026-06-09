package codegraph_test

import (
	"strings"
	"testing"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

func TestMigrationSQLExists(t *testing.T) {
	sql := codegraph.MigrationSQL()
	for _, want := range []string{"code_entities", "code_edges", "CREATE TABLE IF NOT EXISTS"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration SQL missing %q", want)
		}
	}
	if !strings.Contains(sql, "cross_repo_symbols") {
		t.Fatalf("migration SQL missing cross_repo_symbols")
	}
}

func TestMigrationSQLPhase0(t *testing.T) {
	sql := codegraph.MigrationSQL()
	for _, want := range []string{
		"confidence_score",
		"confidence_tier",
		"code_edges_repo_tier",
		"community",
		"cohesion",
		"betweenness",
		"source_url",
		"captured_at",
		"line_start",
		"line_end",
		"code_hyperedges",
		"code_hyperedge_members",
		"code_hyperedges_src",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("phase0 migration SQL missing %q", want)
		}
	}
}
