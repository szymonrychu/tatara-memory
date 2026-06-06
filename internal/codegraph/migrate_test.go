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
