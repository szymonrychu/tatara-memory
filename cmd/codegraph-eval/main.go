// Command codegraph-eval pushes a synthetic fixture graph into a live
// tatara-memory deployment, runs the golden /code/* traversal cases, and reports
// recall@k / MRR (ranked lookups) and precision / recall / F1 (deterministic
// traversals). It exits non-zero when aggregate recall@k falls below a
// configurable floor so it can gate in CI. The end-to-end path needs a real
// backend and is run via `make codegraph-eval`, not unit `make test`. It mirrors
// cmd/eval (the memory eval, issue #41) for the code-graph surface.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	cgeval "github.com/szymonrychu/tatara-memory/eval/codegraph"
	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

const defaultRepo = "eval/codegraph-fixture"

type config struct {
	baseURL     string
	token       string
	repo        string
	recallFloor float64
	k           int
	goldenDir   string
	seedDir     string
	metricsFile string
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := parseConfig(os.Args[1:], os.Getenv)
	if err != nil {
		logger.Error("codegraph_eval.config", "action", "parse_config", "error", err.Error())
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("codegraph_eval.failed", "action", "eval", "error", err.Error())
		os.Exit(1)
	}
}

func parseConfig(args []string, getenv func(string) string) (config, error) {
	fs := flag.NewFlagSet("codegraph-eval", flag.ContinueOnError)
	var cfg config
	fs.StringVar(&cfg.baseURL, "base-url", getenv("MEMORY_BASE_URL"), "tatara-memory base URL (env MEMORY_BASE_URL)")
	fs.StringVar(&cfg.token, "token", getenv("MEMORY_TOKEN"), "OIDC bearer token (env MEMORY_TOKEN)")
	fs.StringVar(&cfg.repo, "repo", envStr(getenv, "CODEGRAPH_EVAL_REPO", defaultRepo), "synthetic fixture repo slug (env CODEGRAPH_EVAL_REPO)")
	fs.Float64Var(&cfg.recallFloor, "recall-floor", envFloat(getenv, "EVAL_RECALL_FLOOR", 0.7), "minimum acceptable mean recall@k (env EVAL_RECALL_FLOOR)")
	fs.IntVar(&cfg.k, "k", envInt(getenv, "EVAL_K", 10), "k for recall@k on ranked cases (env EVAL_K)")
	fs.StringVar(&cfg.goldenDir, "golden-dir", getenv("EVAL_GOLDEN_DIR"), "override dir of golden *.json (default embedded)")
	fs.StringVar(&cfg.seedDir, "seed-dir", getenv("EVAL_SEED_DIR"), "override dir of seed *.json (default embedded)")
	fs.StringVar(&cfg.metricsFile, "metrics-file", getenv("EVAL_METRICS_FILE"), "optional Prometheus textfile to write aggregate scores")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}

	if strings.TrimSpace(cfg.baseURL) == "" {
		return config{}, fmt.Errorf("base-url (MEMORY_BASE_URL) is required")
	}
	if strings.TrimSpace(cfg.repo) == "" {
		return config{}, fmt.Errorf("repo must not be empty")
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
	logger.InfoContext(ctx, "codegraph_eval.loaded", "action", "load",
		"golden_cases", len(golden), "seed_entities", len(seed.Entities), "seed_edges", len(seed.Edges))

	if cfg.token == "" {
		logger.WarnContext(ctx, "codegraph_eval.no_token", "action", "load", "msg", "MEMORY_TOKEN empty; requests will be unauthenticated")
	}

	client, err := cgeval.NewClient(cgeval.ClientConfig{
		BaseURL: cfg.baseURL,
		Repo:    cfg.repo,
		Token:   cfg.token,
		Logger:  logger,
	})
	if err != nil {
		return err
	}

	push, err := client.Push(ctx, seed)
	if err != nil {
		return fmt.Errorf("fixture push: %w", err)
	}
	logger.InfoContext(ctx, "codegraph_eval.seed_ready", "action", "seed_ready",
		"repo", cfg.repo, "files", push.Files, "entities_upserted", push.EntitiesUpserted, "edges_upserted", push.EdgesUpserted)

	scores := make([]cgeval.Score, 0, len(golden))
	for _, gc := range golden {
		results, err := client.Run(ctx, gc)
		if err != nil {
			return fmt.Errorf("case %q: %w", gc.Name, err)
		}
		s := cgeval.ScoreCase(gc, results, cfg.k)
		scores = append(scores, s)
		logger.InfoContext(ctx, "codegraph_eval.case",
			"action", "eval_case",
			"name", s.Name,
			"kind", s.Kind,
			"mode", string(s.Mode),
			"recall_at_k", s.RecallAtK,
			"mrr", s.MRR,
			"precision", s.Precision,
			"f1", s.F1,
			"hits", s.Hits,
			"expected", s.Expected,
			"returned", s.Returned,
		)
	}

	sum := cgeval.Summarize(scores)
	pass := sum.MeanRecallAtK >= cfg.recallFloor
	logger.InfoContext(ctx, "codegraph_eval.summary",
		"action", "eval_summary",
		"cases", sum.Cases,
		"k", cfg.k,
		"mean_recall_at_k", sum.MeanRecallAtK,
		"mean_mrr", sum.MeanMRR,
		"mean_precision", sum.MeanPrecision,
		"mean_f1", sum.MeanF1,
		"floor", cfg.recallFloor,
		"pass", pass,
	)

	if cfg.metricsFile != "" {
		if err := writeMetricsFile(cfg.metricsFile, sum, cfg.k, cfg.recallFloor); err != nil {
			return fmt.Errorf("write metrics file: %w", err)
		}
		logger.InfoContext(ctx, "codegraph_eval.metrics_file", "action", "write_metrics", "path", cfg.metricsFile)
	}

	if !pass {
		return fmt.Errorf("aggregate recall@%d %.4f below floor %.4f", cfg.k, sum.MeanRecallAtK, cfg.recallFloor)
	}
	return nil
}

func loadGolden(cfg config) ([]cgeval.GoldenCase, error) {
	if cfg.goldenDir != "" {
		return cgeval.LoadGoldenDir(cfg.goldenDir)
	}
	return cgeval.LoadGolden()
}

func loadSeed(cfg config) (codegraph.GraphPush, error) {
	if cfg.seedDir != "" {
		return cgeval.LoadSeedDir(cfg.seedDir)
	}
	return cgeval.LoadSeed()
}

// writeMetricsFile emits the aggregate scores in Prometheus textfile-collector
// format so the same /metrics infra the memory eval feeds can carry these too.
func writeMetricsFile(path string, sum cgeval.Summary, k int, floor float64) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# HELP codegraph_eval_recall_at_k Mean recall@k over the golden set.\n")
	fmt.Fprintf(&b, "# TYPE codegraph_eval_recall_at_k gauge\n")
	fmt.Fprintf(&b, "codegraph_eval_recall_at_k{k=\"%d\"} %g\n", k, sum.MeanRecallAtK)
	fmt.Fprintf(&b, "# HELP codegraph_eval_mrr Mean reciprocal rank over the golden set.\n")
	fmt.Fprintf(&b, "# TYPE codegraph_eval_mrr gauge\n")
	fmt.Fprintf(&b, "codegraph_eval_mrr %g\n", sum.MeanMRR)
	fmt.Fprintf(&b, "# HELP codegraph_eval_precision Mean set precision over the golden set.\n")
	fmt.Fprintf(&b, "# TYPE codegraph_eval_precision gauge\n")
	fmt.Fprintf(&b, "codegraph_eval_precision %g\n", sum.MeanPrecision)
	fmt.Fprintf(&b, "# HELP codegraph_eval_f1 Mean set F1 over the golden set.\n")
	fmt.Fprintf(&b, "# TYPE codegraph_eval_f1 gauge\n")
	fmt.Fprintf(&b, "codegraph_eval_f1 %g\n", sum.MeanF1)
	fmt.Fprintf(&b, "# HELP codegraph_eval_cases Number of golden cases evaluated.\n")
	fmt.Fprintf(&b, "# TYPE codegraph_eval_cases gauge\n")
	fmt.Fprintf(&b, "codegraph_eval_cases %d\n", sum.Cases)
	fmt.Fprintf(&b, "# HELP codegraph_eval_recall_floor Configured pass/fail floor for recall@k.\n")
	fmt.Fprintf(&b, "# TYPE codegraph_eval_recall_floor gauge\n")
	fmt.Fprintf(&b, "codegraph_eval_recall_floor %g\n", floor)
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func envStr(getenv func(string) string, key, def string) string {
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
