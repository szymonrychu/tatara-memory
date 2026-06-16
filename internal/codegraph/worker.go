package codegraph

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// AnalyticsStore is the subset of the store the worker needs. Implemented by
// *PGStore.
type AnalyticsStore interface {
	DirtyRepos(ctx context.Context, debounceSecs int) ([]string, error)
	RecomputeAnalytics(ctx context.Context, repo string, labeler CommunityLabeler, betweennessMaxNodes int) (RecomputeResult, error)
}

// AnalyticsWorkerConfig configures the debounced recompute worker.
type AnalyticsWorkerConfig struct {
	// Interval is how often the worker scans for dirty repos. Default 30s.
	Interval time.Duration
	// DebounceSecs is how long a repo must be settled (no reconcile) before its
	// analytics are recomputed. Default 60.
	DebounceSecs int
	// BetweennessMaxNodes caps the graph size for betweenness centrality (Brandes
	// O(V*E)). Graphs larger than this skip betweenness (all values 0.0) to avoid
	// unbounded CPU. Default 5000. Set to -1 to disable the cap (not recommended).
	BetweennessMaxNodes int
	// Logger; defaults to slog.Default().
	Logger *slog.Logger
	// Registerer for the worker's Prometheus instruments. nil registers nothing
	// (the metrics struct stays a usable no-op).
	Registerer prometheus.Registerer
	// tickC, when non-nil, replaces the internal ticker (tests inject ticks).
	tickC <-chan time.Time
}

// recomputeMaxConcurrency caps the number of concurrent RecomputeAnalytics
// goroutines (findings 2, 9). Bounded to GOMAXPROCS so CPU-heavy betweenness
// computation does not fan out unboundedly on mass re-ingests.
var recomputeMaxConcurrency = runtime.GOMAXPROCS(0)

// AnalyticsWorker periodically recomputes analytics for dirty, settled repos.
// Single-flight per repo prevents concurrent recomputes of the same repo.
type AnalyticsWorker struct {
	store               AnalyticsStore
	labeler             CommunityLabeler
	interval            time.Duration
	debounceSecs        int
	betweennessMaxNodes int
	log                 *slog.Logger
	metrics             *AnalyticsMetrics
	tickC               <-chan time.Time

	mu       sync.Mutex
	inflight map[string]bool

	// wg tracks in-flight recompute goroutines so Run can drain them on shutdown.
	wg sync.WaitGroup
	// sem bounds the number of goroutines running RecomputeAnalytics concurrently.
	sem chan struct{}
}

// defaultBetweennessMaxNodes is the production cap for Brandes betweenness.
// Repos larger than this skip betweenness (left at 0.0) to avoid O(V*E) blowup.
const defaultBetweennessMaxNodes = 5000

// NewAnalyticsWorker constructs the worker. labeler may be nil (no LLM labels).
func NewAnalyticsWorker(store AnalyticsStore, labeler CommunityLabeler, cfg AnalyticsWorkerConfig) *AnalyticsWorker {
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.DebounceSecs <= 0 {
		cfg.DebounceSecs = 60
	}
	if cfg.BetweennessMaxNodes == 0 {
		cfg.BetweennessMaxNodes = defaultBetweennessMaxNodes
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	concurrency := recomputeMaxConcurrency
	if concurrency < 1 {
		concurrency = 1
	}
	return &AnalyticsWorker{
		store:               store,
		labeler:             labeler,
		interval:            cfg.Interval,
		debounceSecs:        cfg.DebounceSecs,
		betweennessMaxNodes: cfg.BetweennessMaxNodes,
		log:                 cfg.Logger,
		metrics:             NewAnalyticsMetrics(cfg.Registerer),
		tickC:               cfg.tickC,
		inflight:            map[string]bool{},
		sem:                 make(chan struct{}, concurrency),
	}
}

// Run blocks until ctx is canceled, processing dirty repos on each tick.
// On ctx.Done it stops accepting new ticks and waits for all in-flight
// recompute goroutines to finish before returning (findings 2, 9).
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
			w.wg.Wait()
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
		// Claim the repo under the lock before acquiring the semaphore (finding 9).
		// Setting inflight=true here (not only inside recompute) closes the race
		// window where a tick N+1 pre-check observed inflight=false between the
		// goroutine launch and the inner recompute() guard, consuming a slot for an
		// immediate no-op. recompute() still double-checks under its own lock.
		w.mu.Lock()
		if w.inflight[repo] {
			w.mu.Unlock()
			continue
		}
		w.inflight[repo] = true
		w.mu.Unlock()

		// Acquire semaphore slot with ctx awareness so shutdown is not delayed by
		// a saturated sem (finding 8). Go 1.22+ per-iteration loop variables make
		// the old `repo := repo` shadow copy unnecessary (finding 12).
		select {
		case w.sem <- struct{}{}:
		case <-ctx.Done():
			// Clear the claim we just set so DirtyRepos can return this repo on the
			// next tick if it restarts.
			w.mu.Lock()
			delete(w.inflight, repo)
			w.mu.Unlock()
			return
		}
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			defer func() { <-w.sem }()
			if err := w.recompute(ctx, repo); err != nil {
				w.log.Error("analytics recompute", "repo", repo, "err", err)
			}
		}()
	}
}

// recompute runs RecomputeAnalytics for one repo. processOnce sets
// inflight[repo]=true before calling this goroutine and clears it via the defer
// here. The inner guard was removed: processOnce is the single entry point and
// it already holds the inflight claim when this function starts.
func (w *AnalyticsWorker) recompute(ctx context.Context, repo string) error {
	defer func() {
		w.mu.Lock()
		delete(w.inflight, repo)
		w.mu.Unlock()
	}()

	w.metrics.incInFlight()
	defer w.metrics.decInFlight()

	start := time.Now()
	res, err := w.store.RecomputeAnalytics(ctx, repo, w.labeler, w.betweennessMaxNodes)
	dur := time.Since(start)
	w.metrics.observeDuration(dur.Seconds())
	if err != nil {
		w.metrics.incRun(analyticsResultError)
		return err
	}
	w.metrics.incRun(analyticsResultSuccess)
	w.metrics.observeComputeDuration(float64(res.ComputeDurationMs) / 1000.0)
	if res.BetweennessSkipped {
		w.metrics.incBetweennessSkipped()
	}
	w.log.Info("analytics recompute",
		"action", "recompute_analytics",
		"resource_id", repo,
		"repo", repo,
		"entities", res.Entities,
		"communities", res.Communities,
		"nodes", res.NodeCount,
		"edges", res.EdgeCount,
		"betweenness_skipped", res.BetweennessSkipped,
		"compute_duration_ms", res.ComputeDurationMs,
		"duration_ms", dur.Milliseconds(),
	)
	return nil
}
