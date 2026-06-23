package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func TestParseConfig_PushDefaults(t *testing.T) {
	cfg, err := parseConfig(nil, env(map[string]string{
		"MEMORY_BASE_URL": "https://x",
		"EVAL_PUSH_URL":   "http://op:8082/internal/metrics/push",
	}))
	require.NoError(t, err)
	require.Equal(t, "http://op:8082/internal/metrics/push", cfg.pushURL)
	require.Equal(t, "memory-eval", cfg.runID, "run_id defaults to memory-eval when EVAL_RUN_ID unset")

	cfg, err = parseConfig([]string{"-run-id", "tick-7"}, env(map[string]string{"MEMORY_BASE_URL": "https://x"}))
	require.NoError(t, err)
	require.Equal(t, "tick-7", cfg.runID)
}

func TestBuildExposition(t *testing.T) {
	sum := eval.Summary{Cases: 20, MeanRecallAtK: 0.8, MeanMRR: 0.65}
	out := buildExposition(sum, 10, 0.7, true)
	require.Contains(t, out, "memory_eval_recall_at_k{k=\"10\"} 0.8")
	require.Contains(t, out, "memory_eval_mrr 0.65")
	require.Contains(t, out, "memory_eval_cases 20")
	require.Contains(t, out, "memory_eval_recall_floor 0.7")
	require.Contains(t, out, "# TYPE memory_eval_recall_at_k gauge")
	require.Contains(t, out, "memory_eval_pass 1")

	require.Contains(t, buildExposition(sum, 10, 0.7, false), "memory_eval_pass 0")
}

func TestBuildPushURL(t *testing.T) {
	got := buildPushURL("http://op:8082/internal/metrics/push", "tick-7", "memory-eval", "eval-pod-1")
	require.Contains(t, got, "/internal/metrics/push?")
	require.Contains(t, got, "run_id=tick-7")
	require.Contains(t, got, "job=memory-eval")
	require.Contains(t, got, "pod=eval-pod-1")

	// Pod is omitted when empty; an existing query string uses & as separator.
	got = buildPushURL("http://op:8082/x?a=1", "r", "memory-eval", "")
	require.Contains(t, got, "x?a=1&")
	require.NotContains(t, got, "pod=")
}

func TestPushAggregateMetrics(t *testing.T) {
	type capture struct {
		method, path, query, body, ct string
	}
	var got capture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = capture{r.Method, r.URL.Path, r.URL.RawQuery, string(b), r.Header.Get("Content-Type")}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := config{pushURL: srv.URL + "/internal/metrics/push", runID: "tick-7", k: 10, recallFloor: 0.7}
	exposition := buildExposition(eval.Summary{Cases: 20, MeanRecallAtK: 0.8, MeanMRR: 0.65}, cfg.k, cfg.recallFloor, true)
	pushAggregateMetrics(context.Background(), cfg, exposition, slog.New(slog.NewTextHandler(io.Discard, nil)))

	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/internal/metrics/push", got.path)
	require.Contains(t, got.query, "run_id=tick-7")
	require.Contains(t, got.query, "job=memory-eval")
	require.Contains(t, got.body, "memory_eval_recall_at_k{k=\"10\"} 0.8")
	require.Contains(t, got.body, "memory_eval_pass 1")
	require.Contains(t, got.ct, "text/plain")
}

// A push endpoint that is down must not crash the eval or change its result:
// the push is best-effort and only logged.
func TestPushAggregateMetrics_BestEffortOnError(t *testing.T) {
	cfg := config{pushURL: "http://127.0.0.1:1/internal/metrics/push", runID: "tick-7", k: 10}
	exposition := buildExposition(eval.Summary{Cases: 1, MeanRecallAtK: 1, MeanMRR: 1}, cfg.k, 0.7, true)
	require.NotPanics(t, func() {
		pushAggregateMetrics(context.Background(), cfg, exposition, slog.New(slog.NewTextHandler(io.Discard, nil)))
	})
}
