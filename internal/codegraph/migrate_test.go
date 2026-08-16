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

// The concatenation is not the schema. "cohesion" and "degree" both appear in
// MigrationSQL() - 0003 adds them, 0005 and 0006 drop them again - so the
// phase-0 substring assertion above used to pass on "cohesion" while asserting
// the presence of a column the migrations remove (tatara-memory#107). Assert
// the end state instead: the last statement touching each column drops it.
func TestMigrationSQL_ColumnsDroppedByLaterMigrations(t *testing.T) {
	sql := codegraph.MigrationSQL()
	for _, col := range []string{"cohesion", "degree"} {
		last := strings.LastIndex(sql, col)
		if last < 0 {
			t.Fatalf("expected %q to appear in the migration SQL", col)
		}
		stmt := strings.TrimSpace(sql[strings.LastIndex(sql[:last], ";")+1 : last])
		// Both clauses matter: code_communities.cohesion is a real column that
		// 0004 adds on purpose, so "something drops a cohesion" is not the claim.
		if !strings.Contains(stmt, "DROP COLUMN IF EXISTS") || !strings.Contains(stmt, "code_entities") {
			t.Fatalf("code_entities.%s must end up dropped, last statement touching it was %q", col, stmt)
		}
	}
}
