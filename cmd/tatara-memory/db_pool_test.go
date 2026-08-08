package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These guard the client- and server-side bounds added for tatara-memory#89,
// where backends owned by this service accumulated on mem-mtg-pg until all 97
// non-superuser connection slots were held (SQLSTATE 53300).

func TestPGConnConfig_AppliesSessionTimeouts(t *testing.T) {
	cfg := config{
		PGStatementTimeout: 5 * time.Minute,
		PGIdleInTxTimeout:  2 * time.Minute,
	}
	c, err := pgConnConfig("postgres://u:p@h:5432/d?sslmode=disable", cfg)
	require.NoError(t, err)

	require.Equal(t, "300000", c.RuntimeParams["statement_timeout"],
		"statement_timeout must be sent in ms: it is the only bound that survives the client disappearing mid-statement")
	require.Equal(t, "120000", c.RuntimeParams["idle_in_transaction_session_timeout"],
		"idle_in_transaction_session_timeout must be sent in ms: it is what reaps a transaction whose client is gone")
}

func TestPGConnConfig_DSNOverrideWins(t *testing.T) {
	cfg := config{PGStatementTimeout: 5 * time.Minute, PGIdleInTxTimeout: 2 * time.Minute}
	c, err := pgConnConfig(
		"postgres://u:p@h:5432/d?sslmode=disable&statement_timeout=7000&idle_in_transaction_session_timeout=8000",
		cfg)
	require.NoError(t, err)

	require.Equal(t, "7000", c.RuntimeParams["statement_timeout"])
	require.Equal(t, "8000", c.RuntimeParams["idle_in_transaction_session_timeout"])
}

func TestPGConnConfig_ZeroDisablesTheTimeout(t *testing.T) {
	c, err := pgConnConfig("postgres://u:p@h:5432/d?sslmode=disable", config{})
	require.NoError(t, err)

	require.NotContains(t, c.RuntimeParams, "statement_timeout")
	require.NotContains(t, c.RuntimeParams, "idle_in_transaction_session_timeout")
}

func TestPGConnConfig_RejectsUnparseableDSN(t *testing.T) {
	_, err := pgConnConfig("://nonsense", config{})
	require.Error(t, err)
}

// Pool sizing and lifetimes are applied by newAppWithDeps, not openDB, so they
// land in one place regardless of which dbOpener produced the handle
// (tatara-memory#82). openDB's remaining job is carrying the session timeouts,
// covered by the pgConnConfig tests above.
func TestNewAppWithDeps_AppliesPoolBounds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			_, _ = w.Write([]byte(`{"issuer":"http://` + r.Host + `","jwks_uri":"http://x/jwks"}`)) //nolint:gosec
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cfg := config{
		HTTPAddr:                "127.0.0.1:0",
		PGDSN:                   "fake",
		LightRAGBaseURL:         srv.URL,
		OIDCIssuer:              srv.URL,
		OIDCAudience:            "tatara-memory",
		WorkerPoolSize:          1,
		LogLevel:                "info",
		DBMaxOpenConns:          7,
		DBMaxIdleConns:          3,
		DBConnMaxLifetime:       30 * time.Minute,
		DBConnMaxIdleTime:       5 * time.Minute,
		AnalyticsMaxConcurrency: 1,
	}
	a, err := newAppWithDeps(context.Background(), cfg, fakeDeps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.shutdown(context.Background()) })

	// database/sql only exposes MaxOpenConnections on Stats; the idle and
	// lifetime settings are asserted through validate() + the config wiring.
	require.Equal(t, 7, a.db.Stats().MaxOpenConnections)
}

func TestConfigValidate_RejectsIncoherentPoolBounds(t *testing.T) {
	base := func() config {
		return config{
			PGDSN:                   "x",
			LightRAGBaseURL:         "y",
			WorkerPoolSize:          1,
			DBMaxOpenConns:          10,
			DBMaxIdleConns:          2,
			AnalyticsMaxConcurrency: 1,
			DBWaitInterval:          2 * time.Second,
		}
	}
	require.NoError(t, base().validate())

	cases := map[string]func(*config){
		"zero max open":         func(c *config) { c.DBMaxOpenConns = 0 },
		"idle exceeds open":     func(c *config) { c.DBMaxIdleConns = 11 },
		"negative lifetime":     func(c *config) { c.DBConnMaxLifetime = -time.Second },
		"negative idle time":    func(c *config) { c.DBConnMaxIdleTime = -time.Second },
		"negative stmt":         func(c *config) { c.PGStatementTimeout = -time.Second },
		"negative idle in tx":   func(c *config) { c.PGIdleInTxTimeout = -time.Second },
		"negative recompute":    func(c *config) { c.AnalyticsRecomputeTimeout = -time.Second },
		"unset analytics conc":  func(c *config) { c.AnalyticsMaxConcurrency = 0 },
		"negative analytics cc": func(c *config) { c.AnalyticsMaxConcurrency = -1 },
		"negative db wait":      func(c *config) { c.DBWaitTimeout = -time.Second },
		// A non-positive retry interval would hot-loop the startup wait against
		// a database that is already struggling (tatara-memory#102).
		"zero db wait interval":     func(c *config) { c.DBWaitInterval = 0 },
		"negative db wait interval": func(c *config) { c.DBWaitInterval = -time.Second },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := base()
			mutate(&c)
			require.Error(t, c.validate())
		})
	}
}

// TestNewApp_ExportsDBPoolMetrics is the observability half of the #89 fix: the
// incident had to be reconstructed from the database side because this process
// published nothing about its own connection pool. These families are what an
// alert on pool saturation reads.
func TestNewApp_ExportsDBPoolMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			_, _ = w.Write([]byte(`{"issuer":"http://` + r.Host + `","jwks_uri":"http://x/jwks"}`)) //nolint:gosec
			return
		}
		w.WriteHeader(http.StatusOK)
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
	t.Cleanup(func() { _ = a.shutdown(context.Background()) })

	fams, err := a.reg.Gather()
	require.NoError(t, err)
	got := map[string]bool{}
	for _, f := range fams {
		got[f.GetName()] = true
	}
	for _, want := range []string{
		"go_sql_max_open_connections",
		"go_sql_open_connections",
		"go_sql_in_use_connections",
		"go_sql_idle_connections",
		"go_sql_wait_count_total",
		"go_sql_wait_duration_seconds_total",
	} {
		require.True(t, got[want], "missing pool metric %q; without it, pool saturation is invisible until the database refuses logins", want)
	}
}

func TestLoadConfig_PoolDefaults(t *testing.T) {
	t.Setenv("PG_DSN", "postgres://u:p@h:5432/d")
	t.Setenv("LIGHTRAG_BASE_URL", "http://lr:9621")

	cfg, err := loadConfig(nil)
	require.NoError(t, err)
	require.NoError(t, cfg.validate())

	// 20/5 are tatara-memory#82's deliberate sizing; #89's own 10/2 proposal was
	// dropped in favour of them when the two fixes were merged.
	require.Equal(t, 20, cfg.DBMaxOpenConns)
	require.Equal(t, 5, cfg.DBMaxIdleConns)
	require.Equal(t, 30*time.Minute, cfg.DBConnMaxLifetime)
	require.Equal(t, 5*time.Minute, cfg.DBConnMaxIdleTime)
	require.Equal(t, 5*time.Minute, cfg.PGStatementTimeout)
	require.Equal(t, 2*time.Minute, cfg.PGIdleInTxTimeout)
	require.Equal(t, 2, cfg.AnalyticsMaxConcurrency)
	require.Equal(t, 10*time.Minute, cfg.AnalyticsRecomputeTimeout)
}
