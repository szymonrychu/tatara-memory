package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"log/slog"

	"github.com/stretchr/testify/require"
)

// fakeDeps satisfies dbOpener using the alwaysOKConnector defined in integration_test.go.
type fakeDeps struct{}

func (fakeDeps) openDB(_ string) (*sql.DB, error) {
	return sql.OpenDB(alwaysOKConnector{}), nil
}

func newAppForTest(t *testing.T) (*app, error) {
	t.Helper()
	db, _ := fakeDeps{}.openDB("")
	return &app{
		log: nil,
		reg: nil,
		db:  db,
		server: &http.Server{ //nolint:gosec
			Addr:              ":0",
			ReadHeaderTimeout: 10 * time.Second,
		},
	}, nil
}

func TestApp_NewAndShutdown(t *testing.T) {
	a, err := newAppForTest(t)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, a.shutdown(ctx))
}

func TestBuildObsAndDB(t *testing.T) {
	cfg := config{ //nolint:gosec
		HTTPAddr:        ":0",
		PGDSN:           "postgres://user:pass@127.0.0.1:1/db?sslmode=disable",
		LightRAGBaseURL: "http://127.0.0.1:9999",
		OIDCIssuer:      "https://example/realms/r",
		OIDCAudience:    "tatara-memory",
		WorkerPoolSize:  1,
		LogLevel:        "info",
	}
	logger, reg, err := buildObs(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, logger)
	require.NotNil(t, reg)
}

func TestNewApp_WithFakes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/.well-known/openid-configuration":
			_, _ = w.Write([]byte(`{"issuer":"http://` + r.Host + `","jwks_uri":"http://x/jwks"}`)) //nolint:gosec
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	cfg := config{
		HTTPAddr:        "127.0.0.1:0",
		PGDSN:           "fake",
		LightRAGBaseURL: srv.URL,
		OIDCIssuer:      srv.URL,
		OIDCAudience:    "tatara-memory",
		WorkerPoolSize:  1,
		LogLevel:        "info",
	}
	a, err := newAppWithDeps(context.Background(), cfg, fakeDeps{})
	require.NoError(t, err)
	require.NotNil(t, a.server)
	require.NoError(t, a.shutdown(context.Background()))
}

func TestApp_Migrate_FailsOnBadDB(t *testing.T) {
	a := &app{db: sql.OpenDB(failingConnector{})}
	require.Error(t, a.migrate(context.Background()))
}

func TestWaitForDB_RetriesThenSucceeds(t *testing.T) {
	n := 0
	ping := func(context.Context) error {
		n++
		if n < 3 {
			return errors.New("refused")
		}
		return nil
	}
	require.NoError(t, waitForDB(context.Background(), ping, time.Second, 5*time.Millisecond))
	require.GreaterOrEqual(t, n, 3)
}

func TestWaitForDB_TimesOut(t *testing.T) {
	ping := func(context.Context) error { return errors.New("refused") }
	require.Error(t, waitForDB(context.Background(), ping, 20*time.Millisecond, 5*time.Millisecond))
}

func TestApp_AnalyticsWorkerCancelOnShutdown(t *testing.T) {
	workerCtx, workerCancel := context.WithCancel(context.Background())
	a := &app{
		analyticsCancel: workerCancel,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, a.shutdown(ctx))
	// After shutdown the analytics context must be cancelled.
	require.Error(t, workerCtx.Err(), "analytics context should be cancelled after shutdown")
}

// TestApp_Shutdown_SurfacesServerError verifies that a timed-out server.Shutdown is
// returned by app.shutdown (finding 3: shutdown must not swallow drain errors).
func TestApp_Shutdown_SurfacesServerError(t *testing.T) {
	// Start an HTTP server with a handler that blocks until unblocked.
	blockCh := make(chan struct{})

	srv := &http.Server{ //nolint:gosec
		Addr: "127.0.0.1:0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-blockCh
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := newListener("127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }() //nolint:errcheck

	// Send a request that will hold the connection open inside the handler.
	go func() {
		_, _ = http.Get("http://" + ln.Addr().String() + "/") //nolint:noctx,errcheck
	}()
	time.Sleep(50 * time.Millisecond)

	a := &app{
		log:    slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		server: srv,
	}
	// Pass an already-cancelled context so shutdownCtx (child) is also cancelled;
	// server.Shutdown will return ctx.Err() because the in-flight request is not drained.
	alreadyCancelled, cancel := context.WithCancel(context.Background())
	cancel()
	shutdownErr := a.shutdown(alreadyCancelled)
	// Unblock the handler now that shutdown has returned.
	close(blockCh)
	require.Error(t, shutdownErr, "shutdown must return an error when server.Shutdown times out")
}

// TestApp_Shutdown_NilLoggerNoServerError verifies that a clean shutdown with a nil
// logger does not panic (finding 3: log.Warn is only called when there are errors).
func TestApp_Shutdown_NilLoggerNoServerError(t *testing.T) {
	db, _ := fakeDeps{}.openDB("")
	a := &app{log: nil, db: db}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, a.shutdown(ctx))
}
