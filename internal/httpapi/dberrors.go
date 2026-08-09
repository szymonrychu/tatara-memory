package httpapi

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// SQLSTATEs that mean "the database is unreachable or refusing work right now",
// as opposed to "this statement is wrong". Class 08 (connection exception) is
// matched by prefix; the rest are individually named because their classes also
// contain permanent errors.
const (
	sqlStateTooManyConnections = "53300" // too_many_connections
	sqlStateConfigLimit        = "53400" // configuration_limit_exceeded
	sqlStateAdminShutdown      = "57P01" // admin_shutdown
	sqlStateCrashShutdown      = "57P02" // crash_shutdown
	sqlStateCannotConnectNow   = "57P03" // cannot_connect_now (recovery/startup)
	sqlStateLockNotAvailable   = "55P03" // lock_not_available (lock_timeout fired)
)

// isDBUnavailable reports whether err is a raw database connectivity failure:
// Postgres is down, restarting, out of connection slots, or the connection was
// lost mid-flight.
//
// These reach mapServiceError with no domain wrapper, because the code-graph
// store returns pgx errors verbatim, and before this they fell through to the
// default branch as 500 "internal error". That is wrong in both directions: it
// tells the caller not to retry something entirely retryable, and it counts a
// database blip as a fault of this service on the 5xx-ratio alert. Postgres
// being unreachable for minutes at a time is a routine event here
// (tatara-memory#102: every mem-* stack is re-rendered nightly and all seven
// Postgres pods go NotReady together), so this is expected traffic.
func isDBUnavailable(err error) bool {
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if strings.HasPrefix(pgErr.Code, "08") {
			return true
		}
		switch pgErr.Code {
		case sqlStateTooManyConnections, sqlStateConfigLimit,
			sqlStateAdminShutdown, sqlStateCrashShutdown, sqlStateCannotConnectNow:
			return true
		}
		// Any other SQLSTATE is a statement-level fault (bad SQL, constraint
		// violation, missing table). Those are bugs in this service and must
		// keep reporting as 500 rather than inviting an infinite retry.
		return false
	}
	var connErr *pgconn.ConnectError
	if errors.As(err, &connErr) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// isDBLockContention reports whether err is a lock wait that the session's
// lock_timeout cut short (tatara-memory#98). Somebody else holds the row locks;
// the request can succeed on a later attempt and nothing is broken, so this is
// backpressure (429), not unavailability (503) and certainly not a 5xx fault.
func isDBLockContention(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == sqlStateLockNotAvailable
}
