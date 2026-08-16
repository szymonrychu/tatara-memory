// Package pgmigratetest provides a stub *sql.DB that speaks the pgmigrate
// tracker protocol, so a migration runner can be driven end to end - including
// a second, replay run - without a live Postgres.
//
// It lives in its own package rather than in a _test.go file because the same
// harness is used from three package-external test packages (codegraph_test,
// ingest_test, memory_test) and _test.go helpers cannot cross package
// boundaries.
//
// The stub distinguishes runner bookkeeping from schema DDL exactly rather
// than by heuristic: every statement pgmigrate issues on its own behalf is
// prefixed with pgmigrate.StatementTag, so anything without that prefix is
// migration SQL by definition. That is what makes "the replay issued zero DDL"
// an assertion about the runner rather than about a list of SQL fragments
// somebody has to keep up to date.
package pgmigratetest

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/szymonrychu/tatara-memory/internal/pgmigrate"
)

// Recorder is the control surface for the stub database: it holds the tracker
// rows, answers baseline probes, and records every statement issued.
type Recorder struct {
	mu           sync.Mutex
	applied      map[string]bool
	appliedOrder []string
	probes       map[string]bool
	probedOrder  []string
	stmts        []string
	failOn       map[string]error
}

// New returns a *sql.DB backed by the stub driver, plus the Recorder that
// controls it. The pool is capped at one connection so statement order is
// deterministic.
func New(t *testing.T) (*sql.DB, *Recorder) {
	t.Helper()
	r := &Recorder{
		applied: map[string]bool{},
		probes:  map[string]bool{},
		failOn:  map[string]error{},
	}
	db := sql.OpenDB(connector{r: r})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db, r
}

// SetProbe fixes the answer the stub gives for the baseline probe of the named
// migration. Unset probes answer false, which models a fresh database: nothing
// is baselined and every migration executes.
func (r *Recorder) SetProbe(name string, present bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probes[name] = present
}

// FailOn makes any statement containing fragment return err instead of running.
func (r *Recorder) FailOn(fragment string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failOn[fragment] = err
}

// Reset clears the recorded statement log. Tracker rows and probe answers are
// kept, so a Reset between two Migrate calls isolates the replay run.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stmts = nil
}

// Statements returns every statement issued since the last Reset, in order.
func (r *Recorder) Statements() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.stmts...)
}

// MigrationStatements returns the statements that were NOT runner bookkeeping,
// i.e. the migration SQL the runner actually executed against the schema.
func (r *Recorder) MigrationStatements() []string {
	var out []string
	for _, s := range r.Statements() {
		if !strings.HasPrefix(strings.TrimSpace(s), pgmigrate.StatementTag) {
			out = append(out, s)
		}
	}
	return out
}

// Applied returns the tracker rows in the order they were committed.
func (r *Recorder) Applied() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.appliedOrder...)
}

// Probed returns the migration names whose baseline probe was consulted, in
// order. A migration missing from this list shipped without a probe.
func (r *Recorder) Probed() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.probedOrder...)
}

// transactionUnsafe are the statements that cannot run inside a transaction
// block. pgmigrate wraps every migration body in one, so any of these fails at
// boot with SQLSTATE 25001 and leaves the pod permanently unready.
var transactionUnsafe = []string{"CONCURRENTLY", "VACUUM", "ALTER SYSTEM"}

// RequireTransactionSafe fails the test if sql contains a statement that
// Postgres refuses to run inside a transaction block. The stub driver accepts
// any SQL, so nothing else in a unit suite can catch this.
func RequireTransactionSafe(t *testing.T, sql string) {
	t.Helper()
	upper := strings.ToUpper(sql)
	for _, bad := range transactionUnsafe {
		if strings.Contains(upper, bad) {
			t.Fatalf("migration SQL contains %q, which cannot run inside the transaction pgmigrate opens per migration (SQLSTATE 25001)", bad)
		}
	}
}

func (r *Recorder) record(q string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stmts = append(r.stmts, q)
}

func (r *Recorder) errFor(q string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for frag, err := range r.failOn {
		if strings.Contains(q, frag) {
			return err
		}
	}
	return nil
}

func (r *Recorder) isApplied(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.applied[name]
}

// probe answers a baseline probe. An unregistered probe answers false, which
// models a fresh database: nothing is baselined and every migration executes.
func (r *Recorder) probe(q string) (bool, bool) {
	q = strings.TrimSpace(q)
	prefix := pgmigrate.ProbeTag("")
	if !strings.HasPrefix(q, prefix) {
		return false, false
	}
	name := strings.TrimSpace(strings.SplitN(strings.TrimPrefix(q, prefix), "\n", 2)[0])
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probedOrder = append(r.probedOrder, name)
	return r.probes[name], true
}

func (r *Recorder) commit(names []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range names {
		if r.applied[n] {
			continue
		}
		r.applied[n] = true
		r.appliedOrder = append(r.appliedOrder, n)
	}
}

type connector struct{ r *Recorder }

func (c connector) Connect(context.Context) (driver.Conn, error) { return &conn{r: c.r}, nil }
func (c connector) Driver() driver.Driver                        { return stubDriver{} }

type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("pgmigratetest: Open is unreachable, the stub uses driver.Connector")
}

type conn struct {
	r  *Recorder
	tx *tx
}

func (*conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("pgmigratetest: Prepare unsupported (QueryerContext/ExecerContext are used)")
}
func (*conn) Close() error                             { return nil }
func (*conn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (c *conn) Begin() (driver.Tx, error) { return c.BeginTx(context.Background(), driver.TxOptions{}) }

func (c *conn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.tx = &tx{c: c}
	return c.tx, nil
}

func (c *conn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.r.record(q)
	if err := c.r.errFor(q); err != nil {
		return nil, err
	}
	if strings.HasPrefix(strings.TrimSpace(q), pgmigrate.StampTag) {
		name, err := stringArg(args)
		if err != nil {
			return nil, err
		}
		// The runner must stamp inside the migration's own transaction, or a
		// migration that fails after its DDL would still be recorded as applied.
		if c.tx == nil {
			return nil, fmt.Errorf("pgmigratetest: tracker insert for %q was issued outside a transaction", name)
		}
		c.tx.pending = append(c.tx.pending, name)
	}
	return driver.RowsAffected(1), nil
}

func (c *conn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.r.record(q)
	if err := c.r.errFor(q); err != nil {
		return nil, err
	}
	if strings.HasPrefix(strings.TrimSpace(q), pgmigrate.AppliedCheckTag) {
		name, err := stringArg(args)
		if err != nil {
			return nil, err
		}
		return &boolRows{v: c.r.isApplied(name)}, nil
	}
	if present, ok := c.r.probe(q); ok {
		return &boolRows{v: present}, nil
	}
	return nil, fmt.Errorf("pgmigratetest: unexpected query %q", q)
}

type tx struct {
	c       *conn
	pending []string
}

func (t *tx) Commit() error {
	t.c.r.commit(t.pending)
	t.c.tx = nil
	return nil
}

func (t *tx) Rollback() error {
	t.c.tx = nil
	return nil
}

func stringArg(args []driver.NamedValue) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("pgmigratetest: expected exactly one argument, got %d", len(args))
	}
	s, ok := args[0].Value.(string)
	if !ok {
		return "", fmt.Errorf("pgmigratetest: expected a string argument, got %T", args[0].Value)
	}
	return s, nil
}

type boolRows struct {
	v    bool
	done bool
}

func (*boolRows) Columns() []string { return []string{"bool"} }
func (*boolRows) Close() error      { return nil }
func (r *boolRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.v
	return nil
}
