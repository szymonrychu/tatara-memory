package httpapi

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
	"github.com/szymonrychu/tatara-memory/internal/ingest"
)

func TestMapServiceError_CodeGraph(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"entity not found", codegraph.ErrEntityNotFound, http.StatusNotFound},
		{"invalid scope", codegraph.ErrInvalidScope, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			mapServiceError(w, r, c.err)
			require.Equal(t, c.want, w.Code)
		})
	}
}

// TestMapServiceError_RawPostgresErrors covers the errors that reach the
// transport with no domain wrapper at all - a store method returning the pgx
// error verbatim. Before this they all fell through to the default branch and
// were reported as 500 "internal error", which is wrong twice over: it tells
// the caller not to retry something that is entirely retryable, and it lights
// the 5xx-ratio alert (MemoryHigh5xx) for a database blip that the process did
// not cause and cannot fix.
//
// Postgres was unreachable for 4-8 minutes on a nightly cadence
// (tatara-memory#102), and lock_timeout now deliberately aborts writes that
// queue behind a stuck holder (tatara-memory#98), so both classes are expected
// traffic rather than corner cases.
func TestMapServiceError_RawPostgresErrors(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		want  int
		retry bool
	}{
		{"connection failure", &pgconn.PgError{Code: "08006", Message: "connection failure"}, http.StatusServiceUnavailable, true},
		{"connection does not exist", &pgconn.PgError{Code: "08003"}, http.StatusServiceUnavailable, true},
		{"too many connections", &pgconn.PgError{Code: "53300"}, http.StatusServiceUnavailable, true},
		{"admin shutdown", &pgconn.PgError{Code: "57P01"}, http.StatusServiceUnavailable, true},
		{"crash shutdown", &pgconn.PgError{Code: "57P02"}, http.StatusServiceUnavailable, true},
		{"cannot connect now", &pgconn.PgError{Code: "57P03"}, http.StatusServiceUnavailable, true},
		{"bad conn", driver.ErrBadConn, http.StatusServiceUnavailable, true},
		{"conn done", sql.ErrConnDone, http.StatusServiceUnavailable, true},
		{"dial failure", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, http.StatusServiceUnavailable, true},
		{"wrapped", fmt.Errorf("reconcile: %w", &pgconn.PgError{Code: "08006"}), http.StatusServiceUnavailable, true},

		// A lock wait cut short by lock_timeout is contention, not a fault:
		// somebody else holds the row locks and the caller should come back.
		// Reporting it as 5xx would make tatara-memory#98's own fix look like an
		// outage on every dashboard.
		{"lock timeout", &pgconn.PgError{Code: "55P03"}, http.StatusTooManyRequests, true},

		// Not transient: a missing table is a bug in this service, and retrying
		// it forever is worse than reporting it.
		{"undefined table", &pgconn.PgError{Code: "42P01"}, http.StatusInternalServerError, false},
		{"unique violation", &pgconn.PgError{Code: "23505"}, http.StatusInternalServerError, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			mapServiceError(w, r, c.err)
			require.Equal(t, c.want, w.Code)
			if c.retry {
				require.NotEmpty(t, w.Header().Get("Retry-After"),
					"a retryable status must tell the caller when to come back")
			}
		})
	}
}

// TestMapServiceError_DuplicateKey verifies that ErrDuplicateKey maps to 400,
// not 500. A duplicate idempotency key is a permanent client error (same input
// always produces the same key) and must not trigger retries.
func TestMapServiceError_DuplicateKey(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	mapServiceError(w, r, ingest.ErrDuplicateKey)
	require.Equal(t, http.StatusBadRequest, w.Code)
}
