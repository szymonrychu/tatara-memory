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
	require.Empty(t, cfg.OTLPEndpoint)
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
	t.Setenv("OTLP_ENDPOINT", "otel:4317")
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
	require.Equal(t, "otel:4317", cfg.OTLPEndpoint)
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
