package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type config struct {
	HTTPAddr               string
	PGDSN                  string
	LightRAGBaseURL        string
	OIDCIssuer             string
	OIDCAudience           string
	WorkerPoolSize         int
	IngestItemTimeout      time.Duration
	LogLevel               string
	BetweennessMaxNodes    int
	HTTPWriteTimeout       time.Duration
	IngestCreateJobTimeout time.Duration
	MemoryPurgeBudget      time.Duration

	// Shared pool size and the per-class admission budgets sized against it
	// (tatara-memory#79/#80/#82/#87). validate() enforces the relationship.
	DBMaxOpenConns           int
	DBMaxIdleConns           int
	MemoriesBulkMaxInFlight  int
	CodeGraphBulkMaxInFlight int
	AdmissionWait            time.Duration
	AdmissionRetryAfter      time.Duration

	// Connection-lifetime and server-side session bounds (tatara-memory#89).
	// DB_* configure the database/sql pool, PG_* set Postgres session
	// parameters on every connection. See openDB in app.go for why each exists.
	DBConnMaxLifetime  time.Duration
	DBConnMaxIdleTime  time.Duration
	PGStatementTimeout time.Duration
	PGIdleInTxTimeout  time.Duration

	// AnalyticsMaxConcurrency bounds concurrent code-graph analytics recomputes.
	// Each in-flight recompute holds one pooled connection for its write
	// transaction, so this is part of the pool budget validate() enforces.
	AnalyticsMaxConcurrency int
	// AnalyticsRecomputeTimeout bounds one code-graph analytics recompute end to
	// end. 0 disables.
	AnalyticsRecomputeTimeout time.Duration
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envIntOr(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("env %s: %w", key, err)
	}
	return n, nil
}

func envDurationOr(key string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("env %s: %w", key, err)
	}
	return d, nil
}

func loadConfig(args []string) (config, error) {
	wp, err := envIntOr("WORKER_POOL_SIZE", 4)
	if err != nil {
		return config{}, err
	}
	itemTimeout, err := envDurationOr("INGEST_ITEM_TIMEOUT", 60*time.Second)
	if err != nil {
		return config{}, err
	}
	betweennessMaxNodes, err := envIntOr("BETWEENNESS_MAX_NODES", 0) // 0 -> worker default (5000)
	if err != nil {
		return config{}, err
	}
	writeTimeout, err := envDurationOr("HTTP_WRITE_TIMEOUT", 4*time.Minute)
	if err != nil {
		return config{}, err
	}
	createJobTimeout, err := envDurationOr("INGEST_CREATE_JOB_TIMEOUT", 10*time.Second)
	if err != nil {
		return config{}, err
	}
	purgeBudget, err := envDurationOr("MEMORY_PURGE_BUDGET", memory.DefaultPurgeBudget)
	if err != nil {
		return config{}, err
	}
	maxOpenConns, err := envIntOr("DB_MAX_OPEN_CONNS", 20)
	if err != nil {
		return config{}, err
	}
	maxIdleConns, err := envIntOr("DB_MAX_IDLE_CONNS", 5)
	if err != nil {
		return config{}, err
	}
	memoriesBulkMaxInFlight, err := envIntOr("MEMORIES_BULK_MAX_IN_FLIGHT", 4)
	if err != nil {
		return config{}, err
	}
	codeGraphBulkMaxInFlight, err := envIntOr("CODE_GRAPH_BULK_MAX_IN_FLIGHT", 2)
	if err != nil {
		return config{}, err
	}
	admissionWait, err := envDurationOr("ADMISSION_WAIT", 5*time.Second)
	if err != nil {
		return config{}, err
	}
	admissionRetryAfter, err := envDurationOr("ADMISSION_RETRY_AFTER", 5*time.Second)
	if err != nil {
		return config{}, err
	}
	dbConnMaxLifetime, err := envDurationOr("DB_CONN_MAX_LIFETIME", 30*time.Minute)
	if err != nil {
		return config{}, err
	}
	dbConnMaxIdleTime, err := envDurationOr("DB_CONN_MAX_IDLE_TIME", 5*time.Minute)
	if err != nil {
		return config{}, err
	}
	// 5m clears the observed 194s worst case on /code-graph:bulk with headroom;
	// it is a backstop against a statement that never returns, not a latency SLO.
	pgStatementTimeout, err := envDurationOr("PG_STATEMENT_TIMEOUT", 5*time.Minute)
	if err != nil {
		return config{}, err
	}
	// No transaction in this codebase legitimately sits idle mid-transaction for
	// minutes: after the #89 fix the only Go work between a transaction's
	// statements is row marshalling. 2m is far above that and far below the
	// hours-long hold the incident observed.
	pgIdleInTxTimeout, err := envDurationOr("PG_IDLE_IN_TRANSACTION_TIMEOUT", 2*time.Minute)
	if err != nil {
		return config{}, err
	}
	// Analytics is background batch work sharing the request path's pool, so its
	// fan-out is an explicit part of the pool budget rather than the worker's
	// implicit GOMAXPROCS default - see validate().
	analyticsMaxConcurrency, err := envIntOr("ANALYTICS_MAX_CONCURRENCY", 2)
	if err != nil {
		return config{}, err
	}
	// The incident's own recompute took 76.6s; 10m is ~8x that, so a run that
	// hits this is wedged, not slow.
	analyticsRecomputeTimeout, err := envDurationOr("ANALYTICS_RECOMPUTE_TIMEOUT", 10*time.Minute)
	if err != nil {
		return config{}, err
	}
	cfg := config{
		HTTPAddr:               envOr("HTTP_ADDR", ":8080"),
		PGDSN:                  envOr("PG_DSN", ""),
		LightRAGBaseURL:        envOr("LIGHTRAG_BASE_URL", ""),
		OIDCIssuer:             envOr("OIDC_ISSUER", "https://auth.szymonrichert.pl/realms/master"),
		OIDCAudience:           envOr("OIDC_AUDIENCE", "tatara-memory"),
		WorkerPoolSize:         wp,
		IngestItemTimeout:      itemTimeout,
		LogLevel:               envOr("LOG_LEVEL", "info"),
		BetweennessMaxNodes:    betweennessMaxNodes,
		HTTPWriteTimeout:       writeTimeout,
		IngestCreateJobTimeout: createJobTimeout,
		MemoryPurgeBudget:      purgeBudget,

		DBMaxOpenConns:           maxOpenConns,
		DBMaxIdleConns:           maxIdleConns,
		MemoriesBulkMaxInFlight:  memoriesBulkMaxInFlight,
		CodeGraphBulkMaxInFlight: codeGraphBulkMaxInFlight,
		AdmissionWait:            admissionWait,
		AdmissionRetryAfter:      admissionRetryAfter,

		DBConnMaxLifetime:         dbConnMaxLifetime,
		DBConnMaxIdleTime:         dbConnMaxIdleTime,
		PGStatementTimeout:        pgStatementTimeout,
		PGIdleInTxTimeout:         pgIdleInTxTimeout,
		AnalyticsMaxConcurrency:   analyticsMaxConcurrency,
		AnalyticsRecomputeTimeout: analyticsRecomputeTimeout,
	}

	fs := flag.NewFlagSet("tatara-memory", flag.ContinueOnError)
	fs.StringVar(&cfg.HTTPAddr, "http-addr", cfg.HTTPAddr, "HTTP listen address")
	fs.StringVar(&cfg.PGDSN, "pg-dsn", cfg.PGDSN, "Postgres DSN")
	fs.StringVar(&cfg.LightRAGBaseURL, "lightrag-base-url", cfg.LightRAGBaseURL, "LightRAG base URL")
	fs.StringVar(&cfg.OIDCIssuer, "oidc-issuer", cfg.OIDCIssuer, "OIDC issuer URL")
	fs.StringVar(&cfg.OIDCAudience, "oidc-audience", cfg.OIDCAudience, "OIDC audience")
	fs.IntVar(&cfg.WorkerPoolSize, "worker-pool-size", cfg.WorkerPoolSize, "Ingest worker pool size")
	fs.DurationVar(&cfg.IngestItemTimeout, "ingest-item-timeout", cfg.IngestItemTimeout, "Per-item ingest timeout (0 disables)")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "Log level (debug|info|warn|error)")
	fs.IntVar(&cfg.BetweennessMaxNodes, "betweenness-max-nodes", cfg.BetweennessMaxNodes, "Max graph nodes for betweenness centrality (0 = default 5000)")
	fs.DurationVar(&cfg.HTTPWriteTimeout, "http-write-timeout", cfg.HTTPWriteTimeout, "http.Server WriteTimeout: a raw socket write deadline armed before the handler runs, NOT a handler-abort bound (does not cancel r.Context()); code-graph:bulk opts out of it explicitly (0 disables)")
	fs.DurationVar(&cfg.IngestCreateJobTimeout, "ingest-create-job-timeout", cfg.IngestCreateJobTimeout, "Deadline for /memories:bulk's CreateJob transaction, including DB pool acquire (0 disables)")
	fs.DurationVar(&cfg.MemoryPurgeBudget, "memory-purge-budget", cfg.MemoryPurgeBudget, "Budget for one /memories:bulk reconcile purge, including waiting out LightRAG's pipeline lock (0 disables)")
	fs.IntVar(&cfg.DBMaxOpenConns, "db-max-open-conns", cfg.DBMaxOpenConns, "Max open Postgres connections in the shared pool")
	fs.IntVar(&cfg.DBMaxIdleConns, "db-max-idle-conns", cfg.DBMaxIdleConns, "Max idle Postgres connections kept in the shared pool")
	fs.IntVar(&cfg.MemoriesBulkMaxInFlight, "memories-bulk-max-in-flight", cfg.MemoriesBulkMaxInFlight, "Admission-control budget: concurrent POST /memories:bulk requests (0 disables)")
	fs.IntVar(&cfg.CodeGraphBulkMaxInFlight, "code-graph-bulk-max-in-flight", cfg.CodeGraphBulkMaxInFlight, "Admission-control budget: concurrent POST /code-graph:bulk requests (0 disables)")
	fs.DurationVar(&cfg.AdmissionWait, "admission-wait", cfg.AdmissionWait, "How long a bulk request may queue for an admission slot before it is shed with 429")
	fs.DurationVar(&cfg.AdmissionRetryAfter, "admission-retry-after", cfg.AdmissionRetryAfter, "Base Retry-After advertised on a shed (429) response; jittered up to +50% per response")
	fs.DurationVar(&cfg.DBConnMaxLifetime, "db-conn-max-lifetime", cfg.DBConnMaxLifetime, "Max lifetime of a pooled Postgres connection before it is recycled (0 = unlimited)")
	fs.DurationVar(&cfg.DBConnMaxIdleTime, "db-conn-max-idle-time", cfg.DBConnMaxIdleTime, "Max idle time of a pooled Postgres connection before it is closed (0 = unlimited)")
	fs.DurationVar(&cfg.PGStatementTimeout, "pg-statement-timeout", cfg.PGStatementTimeout, "Server-side statement_timeout applied to every connection (0 disables)")
	fs.DurationVar(&cfg.PGIdleInTxTimeout, "pg-idle-in-transaction-timeout", cfg.PGIdleInTxTimeout, "Server-side idle_in_transaction_session_timeout applied to every connection (0 disables)")
	fs.IntVar(&cfg.AnalyticsMaxConcurrency, "analytics-max-concurrency", cfg.AnalyticsMaxConcurrency, "Max concurrent code-graph analytics recomputes; each holds one pooled connection")
	fs.DurationVar(&cfg.AnalyticsRecomputeTimeout, "analytics-recompute-timeout", cfg.AnalyticsRecomputeTimeout, "Deadline for one code-graph analytics recompute (0 disables)")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func (c config) validate() error {
	if c.PGDSN == "" {
		return fmt.Errorf("pg-dsn is required")
	}
	if c.LightRAGBaseURL == "" {
		return fmt.Errorf("lightrag-base-url is required")
	}
	if c.WorkerPoolSize < 1 {
		return fmt.Errorf("worker-pool-size must be >= 1")
	}
	if c.IngestItemTimeout < 0 {
		return fmt.Errorf("ingest-item-timeout must be >= 0")
	}
	if c.HTTPWriteTimeout < 0 {
		return fmt.Errorf("http-write-timeout must be >= 0")
	}
	if c.IngestCreateJobTimeout < 0 {
		return fmt.Errorf("ingest-create-job-timeout must be >= 0")
	}
	if c.MemoryPurgeBudget < 0 {
		return fmt.Errorf("memory-purge-budget must be >= 0")
	}
	if c.DBMaxOpenConns < 1 {
		return fmt.Errorf("db-max-open-conns must be >= 1")
	}
	if c.DBMaxIdleConns < 0 || c.DBMaxIdleConns > c.DBMaxOpenConns {
		return fmt.Errorf("db-max-idle-conns must be between 0 and db-max-open-conns (%d)", c.DBMaxOpenConns)
	}
	if c.MemoriesBulkMaxInFlight < 0 {
		return fmt.Errorf("memories-bulk-max-in-flight must be >= 0")
	}
	if c.CodeGraphBulkMaxInFlight < 0 {
		return fmt.Errorf("code-graph-bulk-max-in-flight must be >= 0")
	}
	if c.AdmissionWait < 0 {
		return fmt.Errorf("admission-wait must be >= 0")
	}
	if c.AdmissionRetryAfter < 0 {
		return fmt.Errorf("admission-retry-after must be >= 0")
	}
	if c.DBConnMaxLifetime < 0 {
		return fmt.Errorf("db-conn-max-lifetime must be >= 0")
	}
	if c.DBConnMaxIdleTime < 0 {
		return fmt.Errorf("db-conn-max-idle-time must be >= 0")
	}
	if c.PGStatementTimeout < 0 {
		return fmt.Errorf("pg-statement-timeout must be >= 0")
	}
	if c.PGIdleInTxTimeout < 0 {
		return fmt.Errorf("pg-idle-in-transaction-timeout must be >= 0")
	}
	// Must be explicit, never 0: it is a term in the pool budget below, and the
	// worker's own fallback for 0 is GOMAXPROCS, which would make that budget
	// depend on the node the pod lands on (tatara-memory#89).
	if c.AnalyticsMaxConcurrency < 1 {
		return fmt.Errorf("analytics-max-concurrency must be >= 1")
	}
	if c.AnalyticsRecomputeTimeout < 0 {
		return fmt.Errorf("analytics-recompute-timeout must be >= 0")
	}
	// The admission budgets exist to keep the shared Postgres pool from being
	// oversubscribed (tatara-memory#82: a long /code-graph:bulk transaction
	// holds one connection for its whole life). Every admitted bulk request,
	// every ingest worker and every in-flight analytics recompute can hold a
	// connection simultaneously, so if those alone can exhaust the pool the
	// budgets are not actually protecting anything - reads, /readyz and the
	// worker pool would starve exactly as before. Refuse to start on such a
	// configuration rather than shipping a limiter that cannot do its job.
	// Skipped when either bulk budget is disabled (0 = unbounded, an explicit
	// operator opt-out): with an unbounded class there is no budget to check.
	//
	// analytics-max-concurrency joined this sum in tatara-memory#89. The
	// original arithmetic omitted the analytics worker, but that worker holds a
	// pooled connection for the whole of each recompute's write transaction
	// exactly like an ingest worker does, so leaving it out understated the
	// reserved total by up to GOMAXPROCS connections - the same class of
	// undercount that let #89's pool exhaustion go unmodelled.
	if c.MemoriesBulkMaxInFlight > 0 && c.CodeGraphBulkMaxInFlight > 0 {
		reserved := c.MemoriesBulkMaxInFlight + c.CodeGraphBulkMaxInFlight + c.WorkerPoolSize + c.AnalyticsMaxConcurrency
		if reserved >= c.DBMaxOpenConns {
			return fmt.Errorf(
				"admission budgets oversubscribe the DB pool: memories-bulk-max-in-flight(%d) + code-graph-bulk-max-in-flight(%d) + worker-pool-size(%d) + analytics-max-concurrency(%d) = %d must be < db-max-open-conns(%d)",
				c.MemoriesBulkMaxInFlight, c.CodeGraphBulkMaxInFlight, c.WorkerPoolSize, c.AnalyticsMaxConcurrency, reserved, c.DBMaxOpenConns)
		}
	}
	return nil
}
