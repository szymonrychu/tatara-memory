package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"
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

	// Postgres pool + connection-lifetime bounds (tatara-memory#89). See openDB
	// in app.go for why each exists.
	PGMaxOpenConns     int
	PGMaxIdleConns     int
	PGConnMaxLifetime  time.Duration
	PGConnMaxIdleTime  time.Duration
	PGStatementTimeout time.Duration
	PGIdleInTxTimeout  time.Duration

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
	pgMaxOpen, err := envIntOr("PG_MAX_OPEN_CONNS", 10)
	if err != nil {
		return config{}, err
	}
	pgMaxIdle, err := envIntOr("PG_MAX_IDLE_CONNS", 2)
	if err != nil {
		return config{}, err
	}
	pgConnMaxLifetime, err := envDurationOr("PG_CONN_MAX_LIFETIME", 30*time.Minute)
	if err != nil {
		return config{}, err
	}
	pgConnMaxIdleTime, err := envDurationOr("PG_CONN_MAX_IDLE_TIME", 5*time.Minute)
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

		PGMaxOpenConns:            pgMaxOpen,
		PGMaxIdleConns:            pgMaxIdle,
		PGConnMaxLifetime:         pgConnMaxLifetime,
		PGConnMaxIdleTime:         pgConnMaxIdleTime,
		PGStatementTimeout:        pgStatementTimeout,
		PGIdleInTxTimeout:         pgIdleInTxTimeout,
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
	fs.IntVar(&cfg.PGMaxOpenConns, "pg-max-open-conns", cfg.PGMaxOpenConns, "Max open Postgres connections in the shared pool")
	fs.IntVar(&cfg.PGMaxIdleConns, "pg-max-idle-conns", cfg.PGMaxIdleConns, "Max idle Postgres connections kept in the pool")
	fs.DurationVar(&cfg.PGConnMaxLifetime, "pg-conn-max-lifetime", cfg.PGConnMaxLifetime, "Max lifetime of a pooled Postgres connection before it is recycled (0 = unlimited)")
	fs.DurationVar(&cfg.PGConnMaxIdleTime, "pg-conn-max-idle-time", cfg.PGConnMaxIdleTime, "Max idle time of a pooled Postgres connection before it is closed (0 = unlimited)")
	fs.DurationVar(&cfg.PGStatementTimeout, "pg-statement-timeout", cfg.PGStatementTimeout, "Server-side statement_timeout applied to every connection (0 disables)")
	fs.DurationVar(&cfg.PGIdleInTxTimeout, "pg-idle-in-transaction-timeout", cfg.PGIdleInTxTimeout, "Server-side idle_in_transaction_session_timeout applied to every connection (0 disables)")
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
	if c.PGMaxOpenConns < 1 {
		return fmt.Errorf("pg-max-open-conns must be >= 1")
	}
	if c.PGMaxIdleConns < 0 {
		return fmt.Errorf("pg-max-idle-conns must be >= 0")
	}
	if c.PGMaxIdleConns > c.PGMaxOpenConns {
		return fmt.Errorf("pg-max-idle-conns must be <= pg-max-open-conns")
	}
	if c.PGConnMaxLifetime < 0 {
		return fmt.Errorf("pg-conn-max-lifetime must be >= 0")
	}
	if c.PGConnMaxIdleTime < 0 {
		return fmt.Errorf("pg-conn-max-idle-time must be >= 0")
	}
	if c.PGStatementTimeout < 0 {
		return fmt.Errorf("pg-statement-timeout must be >= 0")
	}
	if c.PGIdleInTxTimeout < 0 {
		return fmt.Errorf("pg-idle-in-transaction-timeout must be >= 0")
	}
	if c.AnalyticsRecomputeTimeout < 0 {
		return fmt.Errorf("analytics-recompute-timeout must be >= 0")
	}
	return nil
}
