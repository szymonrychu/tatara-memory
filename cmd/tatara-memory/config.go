package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"
)

type config struct {
	HTTPAddr        string
	PGDSN           string
	LightRAGBaseURL string
	OIDCIssuer      string
	OIDCAudience    string
	WorkerPoolSize  int
	ItemTimeout     time.Duration
	LogLevel        string
	OTLPEndpoint    string
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
	it, err := envDurationOr("INGEST_ITEM_TIMEOUT", 5*time.Minute)
	if err != nil {
		return config{}, err
	}
	cfg := config{
		HTTPAddr:        envOr("HTTP_ADDR", ":8080"),
		PGDSN:           envOr("PG_DSN", ""),
		LightRAGBaseURL: envOr("LIGHTRAG_BASE_URL", ""),
		OIDCIssuer:      envOr("OIDC_ISSUER", "https://auth.szymonrichert.pl/realms/master"),
		OIDCAudience:    envOr("OIDC_AUDIENCE", "tatara-memory"),
		WorkerPoolSize:  wp,
		ItemTimeout:     it,
		LogLevel:        envOr("LOG_LEVEL", "info"),
		OTLPEndpoint:    envOr("OTLP_ENDPOINT", ""),
	}

	fs := flag.NewFlagSet("tatara-memory", flag.ContinueOnError)
	fs.StringVar(&cfg.HTTPAddr, "http-addr", cfg.HTTPAddr, "HTTP listen address")
	fs.StringVar(&cfg.PGDSN, "pg-dsn", cfg.PGDSN, "Postgres DSN")
	fs.StringVar(&cfg.LightRAGBaseURL, "lightrag-base-url", cfg.LightRAGBaseURL, "LightRAG base URL")
	fs.StringVar(&cfg.OIDCIssuer, "oidc-issuer", cfg.OIDCIssuer, "OIDC issuer URL")
	fs.StringVar(&cfg.OIDCAudience, "oidc-audience", cfg.OIDCAudience, "OIDC audience")
	fs.IntVar(&cfg.WorkerPoolSize, "worker-pool-size", cfg.WorkerPoolSize, "Ingest worker pool size")
	fs.DurationVar(&cfg.ItemTimeout, "ingest-item-timeout", cfg.ItemTimeout, "Per-item ingest timeout; 0 disables")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "Log level (debug|info|warn|error)")
	fs.StringVar(&cfg.OTLPEndpoint, "otlp-endpoint", cfg.OTLPEndpoint, "OTLP endpoint (empty disables tracing)")
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
	if c.ItemTimeout < 0 {
		return fmt.Errorf("ingest-item-timeout must be >= 0")
	}
	return nil
}
