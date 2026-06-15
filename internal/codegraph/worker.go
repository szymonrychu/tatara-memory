package codegraph

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// AnalyticsStore is the subset of the store the worker needs. Implemented by
// *PGStore.
type AnalyticsStore interface {
	DirtyRepos(ctx context.Context, debounceSecs int) ([]string, error)
	RecomputeAnalytics(ctx context.Context, repo string, labeler CommunityLabeler) (RecomputeResult, error)
}

// AnalyticsWorkerConfig configures the debounced recompute worker.
type AnalyticsWorkerConfig struct {
	// Interval is how often the worker scans for dirty repos. Default 30s.
	Interval time.Duration
	// DebounceSecs is how long a repo must be settled (no reconcile) before its
	// analytics are recomputed. Default 60.
	DebounceSecs int
	// Logger; defaults to slog.Default().
	Logger *slog.Logger
	// Registerer for the worker's Prometheus instruments. nil registers nothing
	// (the metrics struct stays a usable no-op).
	Registerer prometheus.Registerer
	// tickC, when non-nil, replaces the internal ticker (tests inject ticks).
	tickC <-chan time.Time
}

// AnalyticsWorker periodically recomputes analytics for dirty, settled repos.
// Single-flight per repo prevents concurrent recomputes of the same repo.
type AnalyticsWorker struct {
	store        AnalyticsStore
	labeler      CommunityLabeler
	interval     time.Duration
	debounceSecs int
	log          *slog.Logger
	metrics      *AnalyticsMetrics
	tickC        <-chan time.Time

	mu       sync.Mutex
	inflight map[string]bool
}

// NewAnalyticsWorker constructs the worker. labeler may be nil (no LLM labels).
func NewAnalyticsWorker(store AnalyticsStore, labeler CommunityLabeler, cfg AnalyticsWorkerConfig) *AnalyticsWorker {
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.DebounceSecs <= 0 {
		cfg.DebounceSecs = 60
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &AnalyticsWorker{
		store:        store,
		labeler:      labeler,
		interval:     cfg.Interval,
		debounceSecs: cfg.DebounceSecs,
		log:          cfg.Logger,
		metrics:      NewAnalyticsMetrics(cfg.Registerer),
		tickC:        cfg.tickC,
		inflight:     map[string]bool{},
	}
}

// Run blocks until ctx is canceled, processing dirty repos on each tick.
func (w *AnalyticsWorker) Run(ctx context.Context) {
	tickC := w.tickC
	if tickC == nil {
		t := time.NewTicker(w.interval)
		defer t.Stop()
		tickC = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tickC:
			w.processOnce(ctx)
		}
	}
}

func (w *AnalyticsWorker) processOnce(ctx context.Context) {
	repos, err := w.store.DirtyRepos(ctx, w.debounceSecs)
	if err != nil {
		w.log.Error("analytics dirty repos", "err", err)
		return
	}
	w.metrics.setDirtyRepos(len(repos))
	for _, repo := range repos {
		repo := repo
		go func() {
			if err := w.recompute(ctx, repo); err != nil {
				w.log.Error("analytics recompute", "repo", repo, "err", err)
			}
		}()
	}
}

// recompute runs RecomputeAnalytics for one repo under a single-flight guard.
// If a recompute for repo is already running, recompute returns nil immediately.
func (w *AnalyticsWorker) recompute(ctx context.Context, repo string) error {
	w.mu.Lock()
	if w.inflight[repo] {
		w.mu.Unlock()
		return nil
	}
	w.inflight[repo] = true
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.inflight, repo)
		w.mu.Unlock()
	}()

	w.metrics.incInFlight()
	defer w.metrics.decInFlight()

	start := time.Now()
	res, err := w.store.RecomputeAnalytics(ctx, repo, w.labeler)
	dur := time.Since(start)
	w.metrics.observeDuration(dur.Seconds())
	if err != nil {
		w.metrics.incRun(analyticsResultError)
		return err
	}
	w.metrics.incRun(analyticsResultSuccess)
	w.log.Info("analytics recompute",
		"repo", repo,
		"entities", res.Entities,
		"communities", res.Communities,
		"duration_ms", dur.Milliseconds(),
	)
	return nil
}
