package codegraph

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"

	"github.com/szymonrychu/tatara-memory/internal/pgmigrate"
)

//go:embed migrations/0001_codegraph.sql
var migration0001 string

//go:embed migrations/0002_cross_repo_symbols.sql
var migration0002 string

//go:embed migrations/0003_phase0_graphify.sql
var migration0003 string

//go:embed migrations/0004_phase2_semantic.sql
var migration0004 string

//go:embed migrations/0005_audit_fixes.sql
var migration0005 string

//go:embed migrations/0006_audit_r2_fixes.sql
var migration0006 string

// runner is the ordered migration set for this package.
//
// Every migration carries a baseline probe (tatara-memory#107, option B1). The
// code-graph schema predates the tracker, so the production database has the
// full end state of all six and no tracker rows at all; without probes the
// first boot after this lands would replay them - including 0005's ACCESS
// EXCLUSIVE rebuild of code_edges_pkey - during the very rolling upgrade that
// ships the fix. With them the upgrade issues six tracker inserts and no DDL.
//
// Each probe reads the FINAL state of its own migration out of pg_catalog, not
// "does the schema exist", so a database that stopped part-way is not stamped
// as fully migrated: the first probe that fails ends baselining and every
// migration from there on executes for real (see pgmigrate.Runner.Run).
//
// A new migration added here must ship a probe too, or it re-runs on the boot
// after the one that applies it against every database that skipped it.
var runner = pgmigrate.Runner{
	Tracker: "codegraph_schema_migrations",
	Migrations: []pgmigrate.Migration{
		{
			Name: "0001_codegraph",
			SQL:  migration0001,
			AlreadyApplied: pgmigrate.Probe("0001_codegraph", `
SELECT to_regclass('code_entities') IS NOT NULL
   AND to_regclass('code_edges') IS NOT NULL`),
		},
		{
			Name: "0002_cross_repo_symbols",
			SQL:  migration0002,
			AlreadyApplied: pgmigrate.Probe("0002_cross_repo_symbols", `
SELECT to_regclass('cross_repo_symbols') IS NOT NULL`),
		},
		{
			Name: "0003_phase0_graphify",
			SQL:  migration0003,
			AlreadyApplied: pgmigrate.Probe("0003_phase0_graphify", `
SELECT to_regclass('code_hyperedges') IS NOT NULL
   AND to_regclass('code_hyperedge_members') IS NOT NULL
   AND EXISTS (SELECT 1 FROM pg_attribute
                WHERE attrelid = to_regclass('code_edges')
                  AND attname = 'confidence_tier' AND NOT attisdropped)
   AND EXISTS (SELECT 1 FROM pg_attribute
                WHERE attrelid = to_regclass('code_entities')
                  AND attname = 'community' AND NOT attisdropped)`),
		},
		{
			Name: "0004_phase2_semantic",
			SQL:  migration0004,
			AlreadyApplied: pgmigrate.Probe("0004_phase2_semantic", `
SELECT to_regclass('semantic_extractions') IS NOT NULL
   AND to_regclass('code_communities') IS NOT NULL
   AND to_regclass('repo_analytics_state') IS NOT NULL
   AND EXISTS (SELECT 1 FROM pg_attribute
                WHERE attrelid = to_regclass('code_edges')
                  AND attname = 'extractor' AND NOT attisdropped)
   AND EXISTS (SELECT 1 FROM pg_attribute
                WHERE attrelid = to_regclass('code_entities')
                  AND attname = 'extractor' AND NOT attisdropped)
   AND EXISTS (SELECT 1 FROM pg_attribute
                WHERE attrelid = to_regclass('code_hyperedges')
                  AND attname = 'extractor' AND NOT attisdropped)`),
		},
		{
			// The one migration that is genuinely not re-runnable: ADD PRIMARY KEY
			// has no IF NOT EXISTS and the DROP always matches, so a replay is an
			// unconditional index rebuild under ACCESS EXCLUSIVE. The probe compares
			// the primary key's column SET rather than its existence, because the
			// pre-0005 key is also called code_edges_pkey.
			Name: "0005_audit_fixes",
			SQL:  migration0005,
			AlreadyApplied: pgmigrate.Probe("0005_audit_fixes", `
SELECT EXISTS (
           SELECT 1 FROM pg_constraint c
            WHERE c.conrelid = to_regclass('code_edges')
              AND c.contype = 'p'
              AND (SELECT array_agg(a.attname::text ORDER BY a.attname)
                     FROM pg_attribute a
                    WHERE a.attrelid = c.conrelid
                      AND a.attnum = ANY (c.conkey))
                  = ARRAY['extractor', 'from_id', 'relation', 'repo', 'to_id']
       )
   AND NOT EXISTS (SELECT 1 FROM pg_attribute
                    WHERE attrelid = to_regclass('code_entities')
                      AND attname = 'cohesion' AND NOT attisdropped)`),
		},
		{
			// ALTER COLUMN ... TYPE is a no-op on an already-widened column but
			// still takes ACCESS EXCLUSIVE on code_entities.
			Name: "0006_audit_r2_fixes",
			SQL:  migration0006,
			AlreadyApplied: pgmigrate.Probe("0006_audit_r2_fixes", `
SELECT NOT EXISTS (SELECT 1 FROM pg_attribute
                    WHERE attrelid = to_regclass('code_entities')
                      AND attname = 'degree' AND NOT attisdropped)
   AND EXISTS (SELECT 1 FROM pg_attribute
                WHERE attrelid = to_regclass('code_entities')
                  AND attname = 'betweenness' AND NOT attisdropped
                  AND atttypid = 'float8'::regtype)`),
		},
	},
}

// MigrationSQL returns the DDL for the code-graph schema (all migrations concatenated).
func MigrationSQL() string { return runner.SQL() }

// MigrationNames returns the ordered migration names recorded in the tracker.
func MigrationNames() []string { return runner.Names() }

// TrackerTable returns the name of this package's version-tracking table.
func TrackerTable() string { return runner.Tracker }

// Migrate applies the code-graph schema to db via the shared version-tracked
// runner. Already-applied migrations are skipped, so Migrate is safe to call on
// every startup; see internal/pgmigrate for the tracker and baseline rules.
func Migrate(ctx context.Context, db *sql.DB) error {
	if err := runner.Run(ctx, db); err != nil {
		return fmt.Errorf("codegraph migrate: %w", err)
	}
	return nil
}
