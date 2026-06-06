package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	logger, reg, stop, err := buildObs(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, logger)
	require.NotNil(t, reg)
	require.NotNil(t, stop)
	require.NoError(t, stop(context.Background()))
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
