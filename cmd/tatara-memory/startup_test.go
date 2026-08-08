package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These guard tatara-memory#102, where the memory API crash-looped for ~8
// minutes because of a ~4-minute Postgres outage. The gate was
// `waitForDB(ctx, db.PingContext, 60*time.Second, 2*time.Second)` with both
// constants inline; on expiry the error propagated to main's os.Exit(1), and
// CrashLoopBackOff's exponential backoff (10 -> 20 -> 40 -> 80 -> 160 -> 300s)
// then kept the API down strictly LONGER than the dependency outage that
// caused it. The process had a 2s poll loop of its own and threw it away.

// downConnector is a driver.Connector whose Connect always fails: Postgres is
// unreachable for the whole test.
type downConnector struct{}

func (downConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("dial tcp 10.0.0.1:5432: connect: connection refused")
}
func (downConnector) Driver() driver.Driver { return okDriver{} }

type downDeps struct{}

func (downDeps) openDB(string) (*sql.DB, error) { return sql.OpenDB(downConnector{}), nil }

// flakyConnector refuses connections until up is set, then behaves exactly like
// alwaysOKConnector. It models the incident: Postgres is bounced NotReady for a
// few minutes and then comes back on its own.
type flakyConnector struct{ up *atomic.Bool }

func (c flakyConnector) Connect(context.Context) (driver.Conn, error) {
	if !c.up.Load() {
		return nil, errors.New("dial tcp 10.0.0.1:5432: connect: connection refused")
	}
	return okConn{}, nil
}
func (c flakyConnector) Driver() driver.Driver { return okDriver{} }

type flakyDeps struct{ conn flakyConnector }

func (d flakyDeps) openDB(string) (*sql.DB, error) { return sql.OpenDB(d.conn), nil }

// stubUpstreams serves the LightRAG health endpoint and the OIDC discovery
// document, the two non-Postgres dependencies newAppWithDeps reaches out to.
func stubUpstreams(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_, _ = w.Write([]byte(`{"issuer":"http://` + r.Host + `","jwks_uri":"http://x/jwks"}`)) //nolint:gosec
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func getStatus(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// TestNewAppWithDeps_KeepsServingWhilePostgresIsDown is the core regression for
// #102. Startup must not depend on Postgres at all: the process comes up,
// /healthz stays green so the kubelet leaves it alone, and /readyz reports the
// outage. On origin/main this blocks for the hardcoded 60s and then returns
// "wait for postgres: database not reachable within 1m0s", which main() turns
// into os.Exit(1).
func TestNewAppWithDeps_KeepsServingWhilePostgresIsDown(t *testing.T) {
	stub := stubUpstreams(t)
	cfg := config{
		HTTPAddr:        "127.0.0.1:0",
		PGDSN:           "fake",
		LightRAGBaseURL: stub,
		OIDCIssuer:      stub,
		OIDCAudience:    "tatara-memory",
		WorkerPoolSize:  1,
		LogLevel:        "error",
		DBWaitInterval:  10 * time.Millisecond,
	}

	start := time.Now()
	a, err := newAppWithDeps(context.Background(), cfg, downDeps{})
	require.NoError(t, err,
		"a Postgres outage must leave this process running and unready, never exit it (tatara-memory#102)")
	t.Cleanup(func() { _ = a.shutdown(context.Background()) })
	require.Less(t, time.Since(start), 10*time.Second,
		"startup must not block on the database")

	ln, err := newListener(cfg.HTTPAddr)
	require.NoError(t, err)
	go func() { _ = serve(a.server, ln) }()

	base := "http://" + ln.Addr().String()
	require.Equal(t, http.StatusOK, getStatus(t, base+"/healthz"),
		"liveness must stay green: restarting a pod that is merely waiting for its database is what produced the crash loop")
	require.Equal(t, http.StatusServiceUnavailable, getStatus(t, base+"/readyz"),
		"readiness is where a missing database belongs")
}

// TestApp_BecomesReadyWhenPostgresComesBack: recovery happens in place, on the
// process's own poll loop, with no restart. In the incident the API was down
// ~8 min for a ~4 min Postgres gap precisely because recovery was delegated to
// CrashLoopBackOff instead.
func TestApp_BecomesReadyWhenPostgresComesBack(t *testing.T) {
	stub := stubUpstreams(t)
	up := &atomic.Bool{}
	cfg := config{
		HTTPAddr:        "127.0.0.1:0",
		PGDSN:           "fake",
		LightRAGBaseURL: stub,
		OIDCIssuer:      stub,
		OIDCAudience:    "tatara-memory",
		WorkerPoolSize:  1,
		LogLevel:        "error",
		DBWaitInterval:  10 * time.Millisecond,
	}

	a, err := newAppWithDeps(context.Background(), cfg, flakyDeps{conn: flakyConnector{up: up}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.shutdown(context.Background()) })

	require.Error(t, a.startupStatus(), "must not report ready while Postgres is down")

	up.Store(true)
	require.Eventually(t, a.startupReady, 10*time.Second, 10*time.Millisecond,
		"the process must recover in place once Postgres returns, without a restart")
}

// TestApp_ShutdownStopsTheStartupWait: SIGTERM while still waiting for Postgres
// must end the wait, not hang the drain. A stack that is being disabled has to
// come down cleanly.
func TestApp_ShutdownStopsTheStartupWait(t *testing.T) {
	stub := stubUpstreams(t)
	cfg := config{
		HTTPAddr:        "127.0.0.1:0",
		PGDSN:           "fake",
		LightRAGBaseURL: stub,
		OIDCIssuer:      stub,
		OIDCAudience:    "tatara-memory",
		WorkerPoolSize:  1,
		LogLevel:        "error",
		DBWaitInterval:  10 * time.Millisecond,
	}

	a, err := newAppWithDeps(context.Background(), cfg, downDeps{})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, a.shutdown(ctx))

	select {
	case <-a.startupDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the startup wait outlived shutdown")
	}
}
