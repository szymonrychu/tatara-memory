// Command eval seeds a fixed corpus into a live tatara-memory deployment,
// runs the golden retrieval cases, and reports recall@k / MRR. It exits
// non-zero when aggregate recall@k falls below a configurable floor so it can
// gate in CI. The end-to-end path needs a real backend and is run via
// `make eval`, not unit `make test`.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/szymonrychu/tatara-memory/eval"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type config struct {
	baseURL     string
	token       string
	recallFloor float64
	k           int
	goldenDir   string
	seedDir     string
	metricsFile string
	pushURL     string
	runID       string
	jobTimeout  time.Duration
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := parseConfig(os.Args[1:], os.Getenv)
	if err != nil {
		logger.Error("eval.config", "action", "parse_config", "error", err.Error())
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("eval.failed", "action", "eval", "error", err.Error())
		os.Exit(1)
	}
}

func parseConfig(args []string, getenv func(string) string) (config, error) {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	var cfg config
	fs.StringVar(&cfg.baseURL, "base-url", getenv("MEMORY_BASE_URL"), "tatara-memory base URL (env MEMORY_BASE_URL)")
	fs.StringVar(&cfg.token, "token", getenv("MEMORY_TOKEN"), "OIDC bearer token (env MEMORY_TOKEN)")
	fs.Float64Var(&cfg.recallFloor, "recall-floor", envFloat(getenv, "EVAL_RECALL_FLOOR", 0.7), "minimum acceptable mean recall@k (env EVAL_RECALL_FLOOR)")
	fs.IntVar(&cfg.k, "k", envInt(getenv, "EVAL_K", 10), "k for recall@k (env EVAL_K)")
	fs.StringVar(&cfg.goldenDir, "golden-dir", getenv("EVAL_GOLDEN_DIR"), "override dir of golden *.json (default embedded)")
	fs.StringVar(&cfg.seedDir, "seed-dir", getenv("EVAL_SEED_DIR"), "override dir of seed *.json (default embedded)")
	fs.StringVar(&cfg.metricsFile, "metrics-file", getenv("EVAL_METRICS_FILE"), "optional Prometheus textfile to write aggregate scores")
	fs.StringVar(&cfg.pushURL, "push-url", getenv("EVAL_PUSH_URL"), "optional operator push-receiver URL to POST aggregate scores to (env EVAL_PUSH_URL)")
	fs.StringVar(&cfg.runID, "run-id", envDefault(getenv, "EVAL_RUN_ID", "memory-eval"), "run_id identity stamped on pushed metrics (env EVAL_RUN_ID)")
	fs.DurationVar(&cfg.jobTimeout, "job-timeout", envDuration(getenv, "EVAL_JOB_TIMEOUT", 5*time.Minute), "max wait for the seed ingest job (env EVAL_JOB_TIMEOUT)")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}

	if strings.TrimSpace(cfg.baseURL) == "" {
		return config{}, fmt.Errorf("base-url (MEMORY_BASE_URL) is required")
	}
	if cfg.recallFloor < 0 || cfg.recallFloor > 1 {
		return config{}, fmt.Errorf("recall-floor must be in [0,1], got %v", cfg.recallFloor)
	}
	if cfg.k < 1 {
		return config{}, fmt.Errorf("k must be >= 1, got %d", cfg.k)
	}
	return cfg, nil
}

func run(ctx context.Context, cfg config, logger *slog.Logger) error {
	golden, err := loadGolden(cfg)
	if err != nil {
		return err
	}
	seed, err := loadSeed(cfg)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "eval.loaded", "action", "load", "golden_cases", len(golden), "seed_items", len(seed))

	if cfg.token == "" {
		logger.WarnContext(ctx, "eval.no_token", "action", "load", "msg", "MEMORY_TOKEN empty; requests will be unauthenticated")
	}

	client, err := eval.NewClient(eval.ClientConfig{
		BaseURL:    cfg.baseURL,
		Token:      cfg.token,
		Logger:     logger,
		JobTimeout: cfg.jobTimeout,
	})
	if err != nil {
		return err
	}

	jobID, err := client.BulkIngest(ctx, seed)
	if err != nil {
		return fmt.Errorf("seed ingest: %w", err)
	}
	logger.InfoContext(ctx, "eval.seed_ingest", "action", "seed_ingest", "job_id", jobID, "items", len(seed))

	job, err := client.WaitJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("seed ingest wait: %w", err)
	}
	logger.InfoContext(ctx, "eval.seed_ready", "action", "seed_ready", "job_id", jobID, "status", string(job.Status), "done", job.Done, "failed", job.Failed)
	if job.Status == memory.JobStatusFailed {
		return fmt.Errorf("seed ingest job %s failed (done=%d failed=%d)", jobID, job.Done, job.Failed)
	}
	if job.Status == memory.JobStatusPartial {
		logger.WarnContext(ctx, "eval.seed_partial", "action", "seed_ready", "job_id", jobID, "done", job.Done, "failed", job.Failed)
	}

	scores := make([]eval.Score, 0, len(golden))
	for _, c := range golden {
		res, err := client.Query(ctx, memory.Query{Mode: c.Mode, Text: c.Query, TopK: c.TopK})
		if err != nil {
			return fmt.Errorf("query case %q: %w", c.Name, err)
		}
		s := eval.ScoreCase(c, res.Matches, cfg.k)
		scores = append(scores, s)
		logger.InfoContext(ctx, "eval.case",
			"action", "eval_case",
			"name", c.Name,
			"query", c.Query,
			"mode", string(c.Mode),
			"recall_at_k", s.RecallAtK,
			"mrr", s.MRR,
			"hits", s.Hits,
			"expected", s.Expected,
			"matches", len(res.Matches),
		)
	}

	sum := eval.Summarize(scores)
	pass := sum.MeanRecallAtK >= cfg.recallFloor
	logger.InfoContext(ctx, "eval.summary",
		"action", "eval_summary",
		"cases", sum.Cases,
		"k", cfg.k,
		"mean_recall_at_k", sum.MeanRecallAtK,
		"mean_mrr", sum.MeanMRR,
		"floor", cfg.recallFloor,
		"pass", pass,
	)

	exposition := buildExposition(sum, cfg.k, cfg.recallFloor, pass)
	if cfg.metricsFile != "" {
		if err := os.WriteFile(cfg.metricsFile, []byte(exposition), 0o600); err != nil {
			return fmt.Errorf("write metrics file: %w", err)
		}
		logger.InfoContext(ctx, "eval.metrics_file", "action", "write_metrics", "path", cfg.metricsFile)
	}
	if cfg.pushURL != "" {
		// Best-effort: a push failure is logged but does not change the binary's
		// exit code, which stays the recall-floor CI gate (matches the wrapper's
		// best-effort push contract).
		pushAggregateMetrics(ctx, cfg, exposition, logger)
	}

	if !pass {
		return fmt.Errorf("aggregate recall@%d %.4f below floor %.4f", cfg.k, sum.MeanRecallAtK, cfg.recallFloor)
	}
	return nil
}

func loadGolden(cfg config) ([]eval.GoldenCase, error) {
	if cfg.goldenDir != "" {
		return eval.LoadGoldenDir(cfg.goldenDir)
	}
	return eval.LoadGolden()
}

func loadSeed(cfg config) ([]eval.SeedItem, error) {
	if cfg.seedDir != "" {
		return eval.LoadSeedDir(cfg.seedDir)
	}
	return eval.LoadSeed()
}

// evalPushJob is the stable job identity stamped on pushed eval metrics so the
// operator push-receiver keys this producer distinctly from wrapper/ingest runs.
const evalPushJob = "memory-eval"

// buildExposition renders the aggregate scores in Prometheus text-exposition
// format. The same bytes are written to -metrics-file and POSTed to -push-url so
// the textfile and pushed series are always identical.
func buildExposition(sum eval.Summary, k int, floor float64, pass bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# HELP memory_eval_recall_at_k Mean recall@k over the golden set.\n")
	fmt.Fprintf(&b, "# TYPE memory_eval_recall_at_k gauge\n")
	fmt.Fprintf(&b, "memory_eval_recall_at_k{k=\"%d\"} %g\n", k, sum.MeanRecallAtK)
	fmt.Fprintf(&b, "# HELP memory_eval_mrr Mean reciprocal rank over the golden set.\n")
	fmt.Fprintf(&b, "# TYPE memory_eval_mrr gauge\n")
	fmt.Fprintf(&b, "memory_eval_mrr %g\n", sum.MeanMRR)
	fmt.Fprintf(&b, "# HELP memory_eval_cases Number of golden cases evaluated.\n")
	fmt.Fprintf(&b, "# TYPE memory_eval_cases gauge\n")
	fmt.Fprintf(&b, "memory_eval_cases %d\n", sum.Cases)
	fmt.Fprintf(&b, "# HELP memory_eval_recall_floor Configured pass/fail floor for recall@k.\n")
	fmt.Fprintf(&b, "# TYPE memory_eval_recall_floor gauge\n")
	fmt.Fprintf(&b, "memory_eval_recall_floor %g\n", floor)
	passVal := 0
	if pass {
		passVal = 1
	}
	fmt.Fprintf(&b, "# HELP memory_eval_pass 1 when mean recall@k met the floor, else 0.\n")
	fmt.Fprintf(&b, "# TYPE memory_eval_pass gauge\n")
	fmt.Fprintf(&b, "memory_eval_pass %d\n", passVal)
	return b.String()
}

// pushAggregateMetrics POSTs the exposition to the operator push-receiver with a
// stable identity (run_id/job/pod). Unlike the wrapper it does NOT delete on
// exit: the eval is a one-shot snapshot, so deleting immediately would erase it
// before Prometheus could scrape it. The receiver's TTL ages the series out
// instead, keeping the snapshot scrapeable (issue #46 goal: alert on recall@k <
// floor). Best-effort: failures are logged, not fatal.
func pushAggregateMetrics(ctx context.Context, cfg config, exposition string, logger *slog.Logger) {
	endpoint := buildPushURL(cfg.pushURL, cfg.runID, evalPushJob, hostname())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(exposition))
	if err != nil {
		logger.WarnContext(ctx, "eval.push_build", "action", "push_metrics", "error", err.Error())
		return
	}
	req.Header.Set("Content-Type", "text/plain; version=0.0.4")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		logger.WarnContext(ctx, "eval.push_failed", "action", "push_metrics", "url", cfg.pushURL, "error", err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logger.WarnContext(ctx, "eval.push_rejected", "action", "push_metrics", "url", cfg.pushURL, "status", resp.StatusCode)
		return
	}
	logger.InfoContext(ctx, "eval.pushed", "action", "push_metrics", "url", cfg.pushURL, "run_id", cfg.runID, "job", evalPushJob, "status", resp.StatusCode)
}

// buildPushURL appends the identity query parameters the push-receiver keys each
// run by (run_id, job, and optional pod) to the configured push URL.
func buildPushURL(base, runID, job, pod string) string {
	q := url.Values{}
	q.Set("run_id", runID)
	q.Set("job", job)
	if pod != "" {
		q.Set("pod", pod)
	}
	sep := "?"
	if strings.ContainsRune(base, '?') {
		sep = "&"
	}
	return base + sep + q.Encode()
}

// hostname returns the pod name (HOSTNAME in k8s) for the pod identity label,
// falling back to os.Hostname.
func hostname() string {
	if h := os.Getenv("HOSTNAME"); h != "" {
		return h
	}
	h, _ := os.Hostname()
	return h
}

// envDefault returns the env value for key or def when it is unset/empty.
func envDefault(getenv func(string) string, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}

func envFloat(getenv func(string) string, key string, def float64) float64 {
	if v := getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envInt(getenv func(string) string, key string, def int) int {
	if v := getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(getenv func(string) string, key string, def time.Duration) time.Duration {
	if v := getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
