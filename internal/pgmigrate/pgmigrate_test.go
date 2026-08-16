package pgmigrate_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/pgmigrate"
	"github.com/szymonrychu/tatara-memory/internal/pgmigrate/pgmigratetest"
)

func runner(migs ...pgmigrate.Migration) pgmigrate.Runner {
	return pgmigrate.Runner{Tracker: "demo_schema_migrations", Migrations: migs}
}

func plain(name, sql string) pgmigrate.Migration {
	return pgmigrate.Migration{Name: name, SQL: sql}
}

func probed(name, sql string) pgmigrate.Migration {
	return pgmigrate.Migration{
		Name:           name,
		SQL:            sql,
		AlreadyApplied: pgmigrate.Probe(name, "SELECT true"),
	}
}

func TestRun_AppliesEveryMigrationOnAFreshDatabase(t *testing.T) {
	db, rec := pgmigratetest.New(t)
	r := runner(plain("0001_a", "CREATE TABLE a ()"), plain("0002_b", "CREATE TABLE b ()"))

	require.NoError(t, r.Run(context.Background(), db))

	require.Equal(t, []string{"CREATE TABLE a ()", "CREATE TABLE b ()"}, rec.MigrationStatements())
	require.Equal(t, []string{"0001_a", "0002_b"}, rec.Applied())
}

// The whole point of the tracker: a second Run must touch no schema at all.
func TestRun_ReplayIssuesNoDDL(t *testing.T) {
	db, rec := pgmigratetest.New(t)
	r := runner(plain("0001_a", "CREATE TABLE a ()"), plain("0002_b", "ALTER TABLE a ADD PRIMARY KEY (x)"))

	require.NoError(t, r.Run(context.Background(), db))
	rec.Reset()
	require.NoError(t, r.Run(context.Background(), db))

	require.Empty(t, rec.MigrationStatements(), "replay must issue no migration DDL")
}

// B1: a probe reporting the migration's end state already present stamps the
// tracker row without executing the SQL.
func TestRun_BaselineStampsWithoutExecuting(t *testing.T) {
	db, rec := pgmigratetest.New(t)
	rec.SetProbe("0001_a", true)
	rec.SetProbe("0002_b", true)
	r := runner(probed("0001_a", "CREATE TABLE a ()"), probed("0002_b", "ALTER TABLE a ADD PRIMARY KEY (x)"))

	require.NoError(t, r.Run(context.Background(), db))

	require.Empty(t, rec.MigrationStatements(), "a fully baselined database must take no DDL lock")
	require.Equal(t, []string{"0001_a", "0002_b"}, rec.Applied())
}

// Pre-mortem 1: baselining must stop at the first migration whose end state is
// NOT present, and everything from there on must execute for real - otherwise a
// database that stopped part-way is stamped as fully migrated and silently
// never gains the rest of the schema.
func TestRun_BaselineStopsAtFirstGap(t *testing.T) {
	db, rec := pgmigratetest.New(t)
	rec.SetProbe("0001_a", true)
	rec.SetProbe("0002_b", false)
	// 0003's end state IS present, but it must still execute: baselining ended
	// at 0002, so no later probe is trusted.
	rec.SetProbe("0003_c", true)
	r := runner(
		probed("0001_a", "CREATE TABLE a ()"),
		probed("0002_b", "CREATE TABLE b ()"),
		probed("0003_c", "CREATE TABLE c ()"),
	)

	require.NoError(t, r.Run(context.Background(), db))

	require.Equal(t, []string{"CREATE TABLE b ()", "CREATE TABLE c ()"}, rec.MigrationStatements())
	require.Equal(t, []string{"0001_a", "0002_b", "0003_c"}, rec.Applied())
}

// C1: migration DDL must not inherit the request-path lock budget
// (PG_LOCK_TIMEOUT, 30s by default) that pgConnConfig layers onto every pooled
// connection.
func TestRun_RaisesLockAndStatementTimeoutsPerMigration(t *testing.T) {
	db, rec := pgmigratetest.New(t)
	r := runner(plain("0001_a", "CREATE TABLE a ()"))

	require.NoError(t, r.Run(context.Background(), db))

	var session string
	for _, s := range rec.Statements() {
		if strings.Contains(s, "SET LOCAL") {
			session = s
		}
	}
	require.NotEmpty(t, session, "each migration transaction must raise its own timeouts")
	require.Contains(t, session, "SET LOCAL lock_timeout")
	require.Contains(t, session, "SET LOCAL statement_timeout")
	// Must be set before the migration SQL, or the DDL runs on the old budget.
	require.Less(t, indexOf(rec.Statements(), session), indexOf(rec.Statements(), "CREATE TABLE a ()"))
}

func TestRun_RejectsAnUnsafeTrackerName(t *testing.T) {
	db, _ := pgmigratetest.New(t)
	r := pgmigrate.Runner{Tracker: `x"; DROP TABLE code_edges; --`}

	require.Error(t, r.Run(context.Background(), db))
}

// A failed migration must not stamp its tracker row, so the next boot retries
// it rather than skipping it forever.
func TestRun_DoesNotStampAFailedMigration(t *testing.T) {
	db, rec := pgmigratetest.New(t)
	rec.FailOn("CREATE TABLE b ()", errors.New("boom"))
	r := runner(plain("0001_a", "CREATE TABLE a ()"), plain("0002_b", "CREATE TABLE b ()"))

	require.Error(t, r.Run(context.Background(), db))
	require.Equal(t, []string{"0001_a"}, rec.Applied())
}

func indexOf(all []string, want string) int {
	for i, s := range all {
		if s == want {
			return i
		}
	}
	return -1
}
