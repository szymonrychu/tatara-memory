package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/eval"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParseConfig_DefaultsFromEnv(t *testing.T) {
	cfg, err := parseConfig(nil, env(map[string]string{
		"MEMORY_BASE_URL":   "https://memory.example",
		"MEMORY_TOKEN":      "tok",
		"EVAL_RECALL_FLOOR": "0.85",
		"EVAL_K":            "5",
		"EVAL_JOB_TIMEOUT":  "30s",
	}))
	require.NoError(t, err)
	require.Equal(t, "https://memory.example", cfg.baseURL)
	require.Equal(t, "tok", cfg.token)
	require.InDelta(t, 0.85, cfg.recallFloor, 1e-9)
	require.Equal(t, 5, cfg.k)
	require.Equal(t, 30*time.Second, cfg.jobTimeout)
}

func TestParseConfig_FlagsOverrideEnv(t *testing.T) {
	cfg, err := parseConfig(
		[]string{"-base-url", "https://flag", "-k", "3", "-recall-floor", "0.5"},
		env(map[string]string{"MEMORY_BASE_URL": "https://env", "EVAL_K": "9"}),
	)
	require.NoError(t, err)
	require.Equal(t, "https://flag", cfg.baseURL)
	require.Equal(t, 3, cfg.k)
	require.InDelta(t, 0.5, cfg.recallFloor, 1e-9)
}

func TestParseConfig_Defaults(t *testing.T) {
	cfg, err := parseConfig(nil, env(map[string]string{"MEMORY_BASE_URL": "https://x"}))
	require.NoError(t, err)
	require.InDelta(t, 0.7, cfg.recallFloor, 1e-9)
	require.Equal(t, 10, cfg.k)
	require.Equal(t, 5*time.Minute, cfg.jobTimeout)
}

func TestParseConfig_Rejects(t *testing.T) {
	_, err := parseConfig(nil, env(nil))
	require.Error(t, err, "missing base url")

	_, err = parseConfig([]string{"-base-url", "x", "-recall-floor", "1.5"}, env(nil))
	require.Error(t, err, "floor out of range")

	_, err = parseConfig([]string{"-base-url", "x", "-k", "0"}, env(nil))
	require.Error(t, err, "k must be >= 1")
}

func TestWriteMetricsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eval.prom")
	sum := eval.Summary{Cases: 20, MeanRecallAtK: 0.8, MeanMRR: 0.65}
	require.NoError(t, writeMetricsFile(path, sum, 10, 0.7))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	out := string(data)
	require.Contains(t, out, "memory_eval_recall_at_k{k=\"10\"} 0.8")
	require.Contains(t, out, "memory_eval_mrr 0.65")
	require.Contains(t, out, "memory_eval_cases 20")
	require.Contains(t, out, "memory_eval_recall_floor 0.7")
	require.Contains(t, out, "# TYPE memory_eval_recall_at_k gauge")
}
