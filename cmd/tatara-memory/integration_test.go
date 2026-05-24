package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// alwaysOKConnector is a fake driver.Connector whose Ping and Connect succeed.
type alwaysOKConnector struct{}

func (alwaysOKConnector) Connect(_ context.Context) (driver.Conn, error) { return okConn{}, nil }
func (alwaysOKConnector) Driver() driver.Driver                          { return okDriver{} }

type okDriver struct{}

func (okDriver) Open(string) (driver.Conn, error) { return okConn{}, nil }

type okConn struct{}

func (okConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (okConn) Close() error                        { return nil }
func (okConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (okConn) Ping(_ context.Context) error        { return nil }

// fakeAppDeps satisfies dbOpener with an always-OK connector.
type fakeAppDeps struct{}

func (fakeAppDeps) openDB(_ string) (*sql.DB, error) {
	return sql.OpenDB(alwaysOKConnector{}), nil
}

func TestApp_EndToEnd(t *testing.T) {
	lr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/.well-known/openid-configuration":
			_, _ = w.Write([]byte(`{"issuer":"http://` + r.Host + `","jwks_uri":"http://x/jwks"}`)) //nolint:gosec
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(lr.Close)

	cfg := config{
		HTTPAddr:        "127.0.0.1:0",
		PGDSN:           "fake",
		LightRAGBaseURL: lr.URL,
		OIDCIssuer:      lr.URL,
		OIDCAudience:    "tatara-memory",
		WorkerPoolSize:  1,
		LogLevel:        "info",
	}
	a, err := newAppWithDeps(context.Background(), cfg, fakeAppDeps{})
	require.NoError(t, err)
	ln, err := newListener(cfg.HTTPAddr)
	require.NoError(t, err)
	go func() { _ = serve(a.server, ln) }()

	deadline := time.Now().Add(2 * time.Second)
	var resp *http.Response
	for time.Now().Before(deadline) {
		resp, err = http.Get("http://" + ln.Addr().String() + "/healthz") //nolint:noctx
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, err = http.Get("http://" + ln.Addr().String() + "/readyz") //nolint:noctx
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Graceful shutdown: shut down the server and pool cleanly.
	require.NoError(t, a.shutdown(context.Background()))
}
