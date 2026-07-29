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

	DBMaxOpenConns           int
	DBMaxIdleConns           int
	MemoriesBulkMaxInFlight  int
	CodeGraphBulkMaxInFlight int
	AdmissionWait            time.Duration
	AdmissionRetryAfter      time.Duration
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

		DBMaxOpenConns:           maxOpenConns,
		DBMaxIdleConns:           maxIdleConns,
		MemoriesBulkMaxInFlight:  memoriesBulkMaxInFlight,
		CodeGraphBulkMaxInFlight: codeGraphBulkMaxInFlight,
		AdmissionWait:            admissionWait,
		AdmissionRetryAfter:      admissionRetryAfter,
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
	fs.IntVar(&cfg.DBMaxOpenConns, "db-max-open-conns", cfg.DBMaxOpenConns, "Max open Postgres connections in the shared pool")
	fs.IntVar(&cfg.DBMaxIdleConns, "db-max-idle-conns", cfg.DBMaxIdleConns, "Max idle Postgres connections kept in the shared pool")
	fs.IntVar(&cfg.MemoriesBulkMaxInFlight, "memories-bulk-max-in-flight", cfg.MemoriesBulkMaxInFlight, "Admission-control budget: concurrent POST /memories:bulk requests (0 disables)")
	fs.IntVar(&cfg.CodeGraphBulkMaxInFlight, "code-graph-bulk-max-in-flight", cfg.CodeGraphBulkMaxInFlight, "Admission-control budget: concurrent POST /code-graph:bulk requests (0 disables)")
	fs.DurationVar(&cfg.AdmissionWait, "admission-wait", cfg.AdmissionWait, "How long a bulk request may queue for an admission slot before it is shed with 429")
	fs.DurationVar(&cfg.AdmissionRetryAfter, "admission-retry-after", cfg.AdmissionRetryAfter, "Base Retry-After advertised on a shed (429) response; jittered up to +50% per response")
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
	// The admission budgets exist to keep the shared Postgres pool from being
	// oversubscribed (tatara-memory#82: a long /code-graph:bulk transaction
	// holds one connection for its whole life). Every admitted bulk request
	// plus every ingest worker can hold a connection simultaneously, so if
	// those alone can exhaust the pool the budgets are not actually protecting
	// anything - reads, /readyz and the worker pool would starve exactly as
	// before. Refuse to start on such a configuration rather than shipping a
	// limiter that cannot do its job. Skipped when either budget is disabled
	// (0 = unbounded, an explicit operator opt-out).
	if c.MemoriesBulkMaxInFlight > 0 && c.CodeGraphBulkMaxInFlight > 0 {
		if reserved := c.MemoriesBulkMaxInFlight + c.CodeGraphBulkMaxInFlight + c.WorkerPoolSize; reserved >= c.DBMaxOpenConns {
			return fmt.Errorf(
				"admission budgets oversubscribe the DB pool: memories-bulk-max-in-flight(%d) + code-graph-bulk-max-in-flight(%d) + worker-pool-size(%d) = %d must be < db-max-open-conns(%d)",
				c.MemoriesBulkMaxInFlight, c.CodeGraphBulkMaxInFlight, c.WorkerPoolSize, reserved, c.DBMaxOpenConns)
		}
	}
	return nil
}
