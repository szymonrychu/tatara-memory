package httpapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/ingest"
)

// deadlineFakeConn/deadlineFakeTx/deadlineFakeConnector are the same minimal
// database/sql/driver shims as internal/ingest's acquire-timeout test,
// duplicated here (rather than exported from internal/ingest) because
// internal/httpapi must not import internal/ingest's test-only helpers, and
// this is the smallest fake that lets database/sql's real connection-pool
// bookkeeping run without a live Postgres. sql.OpenDB(connector) is used
// instead of sql.Register+sql.Open so each test gets its own isolated
// connector with no shared global driver name.
type deadlineFakeConn struct{}

func (deadlineFakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("deadlineFakeConn: not implemented")
}
func (deadlineFakeConn) Close() error              { return nil }
func (deadlineFakeConn) Begin() (driver.Tx, error) { return deadlineFakeTx{}, nil }

type deadlineFakeTx struct{}

func (deadlineFakeTx) Commit() error   { return nil }
func (deadlineFakeTx) Rollback() error { return nil }

type deadlineFakeConnector struct{}

func (deadlineFakeConnector) Connect(context.Context) (driver.Conn, error) {
	return deadlineFakeConn{}, nil
}
func (deadlineFakeConnector) Driver() driver.Driver { return deadlineFakeStubDriver{} }

type deadlineFakeStubDriver struct{}

func (deadlineFakeStubDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("deadlineFakeStubDriver: Open should be unreachable, test uses driver.Connector")
}

// TestBulkIngest_PoolExhausted_Returns429WithRetryAfter is the client-visible
// regression test for tatara-memory#85/#86: POST /memories:bulk against a
// tatara-memory instance whose DB connection pool is exhausted by a stuck
// transaction (mirroring the leaked backend #86 found on mem-mtg-pg) must
// come back fast with Retry-After, not hang for the ingest client's 300s
// timeout and not surface as 499 (context.Canceled) or 500.
//
// The status is 429, not the 503 this test originally asserted: pool
// exhaustion is saturation, and reporting saturation as a server error is what
// made every concurrent-ingest burst page as an outage (tatara-memory#80).
//
// The whole real stack is wired for this request: chi router ->
// handleBulkIngest -> ingest.Enqueuer -> ingest.PGStore.CreateJob -> a
// *sql.DB capped at one connection whose only slot is held by a
// never-committed transaction, exactly like the incident's transaction
// opened 06:03:47Z that never committed or rolled back.
func TestBulkIngest_PoolExhausted_Returns429WithRetryAfter(t *testing.T) {
	db := sql.OpenDB(deadlineFakeConnector{})
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	// Occupy the pool's only connection in an open, never-released
	// transaction so the handler's own CreateJob can never acquire one.
	// Assertion failures inside the goroutine use assert (not require):
	// require calls t.FailNow, which testing documents must run on the
	// test's own goroutine. The holder is released via t.Context(), which
	// testing cancels when the test completes, so it cannot leak past it.
	acquired := make(chan struct{})
	go func() {
		tx, err := db.BeginTx(context.Background(), nil)
		if !assert.NoError(t, err) {
			close(acquired)
			return
		}
		close(acquired)
		<-t.Context().Done() // released when the test completes
		_ = tx.Rollback()
	}()
	<-acquired

	store := ingest.NewPGStore(db, ingest.WithCreateJobTimeout(100*time.Millisecond))
	enqueuer := ingest.NewEnqueuer(store, nil)

	router := httpapi.NewRouter(httpapi.Config{
		Service: &stubService{},
		Ingest:  enqueuer,
	})
	srv := httptest.NewServer(router)
	defer srv.Close()

	body, err := json.Marshal(map[string]interface{}{
		"items": []map[string]string{{"text": "a"}},
	})
	require.NoError(t, err)

	start := time.Now()
	resp, err := http.Post(srv.URL+"/memories:bulk", "application/json", bytes.NewReader(body))
	elapsed := time.Since(start)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Less(t, elapsed, 5*time.Second,
		"request must fail fast on pool exhaustion, not hang toward the ingest client's 300s timeout")
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode,
		"pool-acquire timeout must map to 429 (errmap.go's DeadlineExceeded branch), not 503, 499 (Canceled) or 500")
	require.Equal(t, "5", resp.Header.Get("Retry-After"),
		"the shed path must carry Retry-After so the ingest client backs off and retries")
}
