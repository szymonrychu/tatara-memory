package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	cgeval "github.com/szymonrychu/tatara-memory/eval/codegraph"
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
	}))
	require.NoError(t, err)
	require.Equal(t, "https://memory.example", cfg.baseURL)
	require.Equal(t, "tok", cfg.token)
	require.Equal(t, defaultRepo, cfg.repo)
	require.InDelta(t, 0.85, cfg.recallFloor, 1e-9)
	require.Equal(t, 5, cfg.k)
}

func TestParseConfig_FlagsOverrideEnv(t *testing.T) {
	cfg, err := parseConfig(
		[]string{"-base-url", "https://flag", "-k", "3", "-recall-floor", "0.5", "-repo", "custom/slug"},
		env(map[string]string{"MEMORY_BASE_URL": "https://env", "EVAL_K": "9"}),
	)
	require.NoError(t, err)
	require.Equal(t, "https://flag", cfg.baseURL)
	require.Equal(t, 3, cfg.k)
	require.InDelta(t, 0.5, cfg.recallFloor, 1e-9)
	require.Equal(t, "custom/slug", cfg.repo)
}

func TestParseConfig_Defaults(t *testing.T) {
	cfg, err := parseConfig(nil, env(map[string]string{"MEMORY_BASE_URL": "https://x"}))
	require.NoError(t, err)
	require.InDelta(t, 0.7, cfg.recallFloor, 1e-9)
	require.Equal(t, 10, cfg.k)
	require.Equal(t, defaultRepo, cfg.repo)
}

func TestParseConfig_Rejects(t *testing.T) {
	_, err := parseConfig(nil, env(nil))
	require.Error(t, err, "missing base url")

	_, err = parseConfig([]string{"-base-url", "x", "-recall-floor", "1.5"}, env(nil))
	require.Error(t, err, "floor out of range")

	_, err = parseConfig([]string{"-base-url", "x", "-k", "0"}, env(nil))
	require.Error(t, err, "k must be >= 1")

	_, err = parseConfig([]string{"-base-url", "x", "-repo", ""}, env(nil))
	require.Error(t, err, "empty repo")
}

func TestWriteMetricsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codegraph-eval.prom")
	sum := cgeval.Summary{Cases: 11, MeanRecallAtK: 0.9, MeanMRR: 0.8, MeanPrecision: 0.95, MeanF1: 0.92}
	require.NoError(t, writeMetricsFile(path, sum, 10, 0.7))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	out := string(data)
	require.Contains(t, out, "codegraph_eval_recall_at_k{k=\"10\"} 0.9")
	require.Contains(t, out, "codegraph_eval_mrr 0.8")
	require.Contains(t, out, "codegraph_eval_precision 0.95")
	require.Contains(t, out, "codegraph_eval_f1 0.92")
	require.Contains(t, out, "codegraph_eval_cases 11")
	require.Contains(t, out, "codegraph_eval_recall_floor 0.7")
	require.Contains(t, out, "# TYPE codegraph_eval_recall_at_k gauge")
}
