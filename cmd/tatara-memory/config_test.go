package main

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadConfig_Defaults(t *testing.T) {
	os.Clearenv()
	cfg, err := loadConfig([]string{})
	require.NoError(t, err)
	require.Equal(t, ":8080", cfg.HTTPAddr)
	require.Equal(t, "https://auth.szymonrichert.pl/realms/master", cfg.OIDCIssuer)
	require.Equal(t, "tatara-memory", cfg.OIDCAudience)
	require.Equal(t, 4, cfg.WorkerPoolSize)
	require.Equal(t, 60*time.Second, cfg.IngestItemTimeout)
	require.Equal(t, "info", cfg.LogLevel)
}

// TestLoadConfig_DBWaitDefaults pins the default that removes the crash loop in
// tatara-memory#102: the startup wait on Postgres is UNLIMITED unless an
// operator asks otherwise. The old behaviour was a hardcoded, non-configurable
// 60s budget that ended in os.Exit(1), so a 4-minute database outage became an
// 8-minute API outage on CrashLoopBackOff's slower backoff.
func TestLoadConfig_DBWaitDefaults(t *testing.T) {
	os.Clearenv()
	cfg, err := loadConfig([]string{})
	require.NoError(t, err)
	require.Equal(t, time.Duration(0), cfg.DBWaitTimeout,
		"the default must be an unlimited wait, not a fixed budget that exits the process")
	require.Equal(t, 2*time.Second, cfg.DBWaitInterval)
}

// TestLoadConfig_PGLockTimeoutDefault pins tatara-memory#98's bound. 30s is a
// LOCK WAIT, not a transaction duration: /code-graph:bulk's own reconcile
// legitimately runs 50-194s, but none of that is spent queued behind another
// session, so waiting this long means someone else's transaction is stuck.
func TestLoadConfig_PGLockTimeoutDefault(t *testing.T) {
	os.Clearenv()
	cfg, err := loadConfig([]string{})
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, cfg.PGLockTimeout)
	require.Less(t, cfg.PGLockTimeout, cfg.PGStatementTimeout,
		"a lock wait must be cut short well before the statement budget, or lock_timeout adds nothing")
}

func TestLoadConfig_DBWaitConfigurable(t *testing.T) {
	os.Clearenv()
	t.Setenv("DB_WAIT_TIMEOUT", "10m")
	t.Setenv("DB_WAIT_INTERVAL", "5s")
	cfg, err := loadConfig([]string{})
	require.NoError(t, err)
	require.Equal(t, 10*time.Minute, cfg.DBWaitTimeout)
	require.Equal(t, 5*time.Second, cfg.DBWaitInterval)

	cfg, err = loadConfig([]string{"--db-wait-timeout", "30s", "--db-wait-interval", "1s"})
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, cfg.DBWaitTimeout)
	require.Equal(t, time.Second, cfg.DBWaitInterval)
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	os.Clearenv()
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("PG_DSN", "postgres://u:p@db:5432/tm?sslmode=disable")
	t.Setenv("LIGHTRAG_BASE_URL", "http://lr:9621")
	t.Setenv("OIDC_ISSUER", "https://idp.example/realms/r")
	t.Setenv("OIDC_AUDIENCE", "svc")
	t.Setenv("WORKER_POOL_SIZE", "8")
	t.Setenv("INGEST_ITEM_TIMEOUT", "90s")
	t.Setenv("LOG_LEVEL", "debug")
	cfg, err := loadConfig([]string{})
	require.NoError(t, err)
	require.Equal(t, ":9090", cfg.HTTPAddr)
	require.Equal(t, "postgres://u:p@db:5432/tm?sslmode=disable", cfg.PGDSN)
	require.Equal(t, "http://lr:9621", cfg.LightRAGBaseURL)
	require.Equal(t, "https://idp.example/realms/r", cfg.OIDCIssuer)
	require.Equal(t, "svc", cfg.OIDCAudience)
	require.Equal(t, 8, cfg.WorkerPoolSize)
	require.Equal(t, 90*time.Second, cfg.IngestItemTimeout)
	require.Equal(t, "debug", cfg.LogLevel)
}

func TestLoadConfig_FlagsBeatEnv(t *testing.T) {
	os.Clearenv()
	t.Setenv("HTTP_ADDR", ":9090")
	cfg, err := loadConfig([]string{"--http-addr", ":7777"})
	require.NoError(t, err)
	require.Equal(t, ":7777", cfg.HTTPAddr)
}

func TestLoadConfig_ValidateRequired(t *testing.T) {
	os.Clearenv()
	cfg, err := loadConfig([]string{})
	require.NoError(t, err)
	require.Error(t, cfg.validate())

	cfg.PGDSN = "postgres://x"
	cfg.LightRAGBaseURL = "http://lr"
	require.NoError(t, cfg.validate())
}

func TestLoadConfig_ValidatePoolSize(t *testing.T) {
	os.Clearenv()
	cfg, err := loadConfig([]string{"--worker-pool-size", "0"})
	require.NoError(t, err)
	cfg.PGDSN = "x"
	cfg.LightRAGBaseURL = "y"
	require.Error(t, cfg.validate())
}

func TestLoadConfig_ItemTimeoutFlagAndDisable(t *testing.T) {
	os.Clearenv()
	cfg, err := loadConfig([]string{"--ingest-item-timeout", "0"})
	require.NoError(t, err)
	require.Equal(t, time.Duration(0), cfg.IngestItemTimeout)
	cfg.PGDSN = "x"
	cfg.LightRAGBaseURL = "y"
	require.NoError(t, cfg.validate()) // 0 disables; still valid

	cfg.IngestItemTimeout = -time.Second
	require.Error(t, cfg.validate())
}

func TestLoadConfig_ItemTimeoutBadEnv(t *testing.T) {
	os.Clearenv()
	t.Setenv("INGEST_ITEM_TIMEOUT", "nope")
	_, err := loadConfig([]string{})
	require.Error(t, err)
}

// The shipped defaults must actually protect the pool: every admission budget,
// every ingest worker and every concurrent analytics recompute must fit inside
// it with room left for reads and /readyz. A default set that fails its own
// validation would ship a limiter that cannot do its job (tatara-memory#82) -
// or worse, a binary that will not boot at all in production while every unit
// test that builds a config by hand still passes.
func TestLoadConfig_AdmissionDefaultsFitThePool(t *testing.T) {
	os.Clearenv()
	cfg, err := loadConfig([]string{})
	require.NoError(t, err)
	require.Equal(t, 20, cfg.DBMaxOpenConns)
	require.Equal(t, 5, cfg.DBMaxIdleConns)
	require.Equal(t, 4, cfg.MemoriesBulkMaxInFlight)
	require.Equal(t, 2, cfg.CodeGraphBulkMaxInFlight)
	require.Equal(t, 5*time.Second, cfg.AdmissionWait)
	require.Equal(t, 5*time.Second, cfg.AdmissionRetryAfter)
	require.Equal(t, 4, cfg.WorkerPoolSize)
	require.Equal(t, 2, cfg.AnalyticsMaxConcurrency)

	cfg.PGDSN = "x"
	cfg.LightRAGBaseURL = "y"
	require.NoError(t, cfg.validate())
	require.Less(t,
		cfg.MemoriesBulkMaxInFlight+cfg.CodeGraphBulkMaxInFlight+cfg.WorkerPoolSize+cfg.AnalyticsMaxConcurrency,
		cfg.DBMaxOpenConns)
}

// TestConfigDefaultsPassOversubscriptionCheck is the boot test for the merged
// configuration of tatara-memory#82 (admission control) and #89 (analytics
// connection holds). The two fixes landed independently on the same pool, and
// the failure mode being guarded is a merge that is green in unit tests but
// dies on the startup oversubscription check in production. This exercises the
// exact path run() takes - loadConfig with no env and no flags, then validate -
// and pins the arithmetic so a future default bump cannot quietly cross the
// line. Defaults: 4 + 2 + 4 + 2 = 12 < 20.
func TestConfigDefaultsPassOversubscriptionCheck(t *testing.T) {
	os.Clearenv()
	t.Setenv("PG_DSN", "postgres://u:p@h:5432/d?sslmode=disable")
	t.Setenv("LIGHTRAG_BASE_URL", "http://lr:9621")

	cfg, err := loadConfig(nil)
	require.NoError(t, err)
	require.NoError(t, cfg.validate(),
		"shipped defaults must boot: this check runs before the HTTP server starts, so failing it is a crashloop, not a degraded mode")

	reserved := cfg.MemoriesBulkMaxInFlight + cfg.CodeGraphBulkMaxInFlight + cfg.WorkerPoolSize + cfg.AnalyticsMaxConcurrency
	require.Equal(t, 12, reserved)
	require.Equal(t, 20, cfg.DBMaxOpenConns)
	require.Equal(t, 8, cfg.DBMaxOpenConns-reserved,
		"headroom left for /code/* reads, /readyz and job creation")
}

// The analytics worker holds a pooled connection for the whole of each
// recompute's write transaction, exactly like an ingest worker, so it belongs
// in the pool budget. It was missing from the original check.
func TestLoadConfig_AnalyticsConcurrencyCountsAgainstThePool(t *testing.T) {
	os.Clearenv()
	cfg, err := loadConfig([]string{
		"--db-max-open-conns", "13",
		"--memories-bulk-max-in-flight", "4",
		"--code-graph-bulk-max-in-flight", "2",
		"--worker-pool-size", "4",
		"--analytics-max-concurrency", "2",
	})
	require.NoError(t, err)
	cfg.PGDSN = "x"
	cfg.LightRAGBaseURL = "y"

	// 4+2+4+2 = 12 < 13: fits.
	require.NoError(t, cfg.validate())

	// One more analytics slot makes it 13, which is not strictly less than 13.
	// Without the analytics term this would have been 10 < 13 and accepted.
	cfg.AnalyticsMaxConcurrency = 3
	err = cfg.validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "oversubscribe the DB pool")
	require.Contains(t, err.Error(), "analytics-max-concurrency(3)")
}

// 0 would fall back to the worker's GOMAXPROCS default, which makes the pool
// budget depend on the node the pod lands on. The server must always carry an
// explicit number.
func TestLoadConfig_RejectsUnsetAnalyticsConcurrency(t *testing.T) {
	os.Clearenv()
	cfg, err := loadConfig([]string{"--analytics-max-concurrency", "0"})
	require.NoError(t, err)
	cfg.PGDSN = "x"
	cfg.LightRAGBaseURL = "y"
	err = cfg.validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "analytics-max-concurrency must be >= 1")
}

func TestLoadConfig_AdmissionEnvOverrides(t *testing.T) {
	os.Clearenv()
	t.Setenv("DB_MAX_OPEN_CONNS", "30")
	t.Setenv("DB_MAX_IDLE_CONNS", "8")
	t.Setenv("MEMORIES_BULK_MAX_IN_FLIGHT", "6")
	t.Setenv("CODE_GRAPH_BULK_MAX_IN_FLIGHT", "3")
	t.Setenv("ADMISSION_WAIT", "2s")
	t.Setenv("ADMISSION_RETRY_AFTER", "15s")
	cfg, err := loadConfig([]string{})
	require.NoError(t, err)
	require.Equal(t, 30, cfg.DBMaxOpenConns)
	require.Equal(t, 8, cfg.DBMaxIdleConns)
	require.Equal(t, 6, cfg.MemoriesBulkMaxInFlight)
	require.Equal(t, 3, cfg.CodeGraphBulkMaxInFlight)
	require.Equal(t, 2*time.Second, cfg.AdmissionWait)
	require.Equal(t, 15*time.Second, cfg.AdmissionRetryAfter)
}

// A configuration whose bulk budgets plus workers can exhaust the pool is
// refused at startup: it looks like admission control but reintroduces exactly
// the starvation the limiter exists to prevent.
func TestLoadConfig_RejectsOversubscribedAdmissionBudgets(t *testing.T) {
	os.Clearenv()
	cfg, err := loadConfig([]string{
		"--db-max-open-conns", "8",
		"--memories-bulk-max-in-flight", "4",
		"--code-graph-bulk-max-in-flight", "2",
		"--worker-pool-size", "4",
	})
	require.NoError(t, err)
	cfg.PGDSN = "x"
	cfg.LightRAGBaseURL = "y"
	err = cfg.validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "oversubscribe the DB pool")

	// 0 disables a budget entirely; that is an explicit opt-out, not a
	// misconfiguration, so the oversubscription check does not apply.
	cfg.MemoriesBulkMaxInFlight = 0
	require.NoError(t, cfg.validate())
}

func TestLoadConfig_RejectsNegativeAdmissionValues(t *testing.T) {
	os.Clearenv()
	base, err := loadConfig([]string{})
	require.NoError(t, err)
	base.PGDSN = "x"
	base.LightRAGBaseURL = "y"
	require.NoError(t, base.validate())

	cases := map[string]func(c *config){
		"negative memories budget":  func(c *config) { c.MemoriesBulkMaxInFlight = -1 },
		"negative codegraph budget": func(c *config) { c.CodeGraphBulkMaxInFlight = -1 },
		"negative wait":             func(c *config) { c.AdmissionWait = -time.Second },
		"negative retry after":      func(c *config) { c.AdmissionRetryAfter = -time.Second },
		"zero pool":                 func(c *config) { c.DBMaxOpenConns = 0 },
		"idle above open":           func(c *config) { c.DBMaxIdleConns = c.DBMaxOpenConns + 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			require.Error(t, cfg.validate())
		})
	}
}
