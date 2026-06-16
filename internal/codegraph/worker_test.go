package codegraph

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// fakeAnalyticsStore satisfies AnalyticsStore for worker tests.
type fakeAnalyticsStore struct {
	mu           sync.Mutex
	dirty        []string
	debounceSeen int
	recomputed   []string
	// blockCh, when non-nil, causes RecomputeAnalytics to block until closed.
	blockCh chan struct{}
}

func (f *fakeAnalyticsStore) DirtyRepos(_ context.Context, debounceSecs int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.debounceSeen = debounceSecs
	out := make([]string, len(f.dirty))
	copy(out, f.dirty)
	return out, nil
}

func (f *fakeAnalyticsStore) RecomputeAnalytics(_ context.Context, repo string, _ CommunityLabeler, _ int) (RecomputeResult, error) {
	if f.blockCh != nil {
		<-f.blockCh
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recomputed = append(f.recomputed, repo)
	return RecomputeResult{}, nil
}

func (f *fakeAnalyticsStore) recomputedRepos() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.recomputed))
	copy(out, f.recomputed)
	return out
}

func TestWorker_TickRecomputesDirtyRepos(t *testing.T) {
	tickC := make(chan time.Time, 1)
	store := &fakeAnalyticsStore{dirty: []string{"repo/a", "repo/b"}}

	w := NewAnalyticsWorker(store, nil, AnalyticsWorkerConfig{
		DebounceSecs: 45,
		tickC:        tickC,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()

	// Inject one tick and wait for both repos to be recomputed.
	tickC <- time.Now()

	require.Eventually(t, func() bool {
		return len(store.recomputedRepos()) == 2
	}, 2*time.Second, 10*time.Millisecond)

	repos := store.recomputedRepos()
	require.ElementsMatch(t, []string{"repo/a", "repo/b"}, repos)
	require.Equal(t, 45, store.debounceSeen)

	cancel()
	<-done
}

// blockingAnalyticsStore wraps fakeAnalyticsStore but makes RecomputeAnalytics
// signal entry (inRecompute) and block until blockCh is closed, so the
// single-flight test can orchestrate concurrency deterministically.
type blockingAnalyticsStore struct {
	*fakeAnalyticsStore
	inRecompute chan struct{}
	blockCh     chan struct{}
}

func (b *blockingAnalyticsStore) RecomputeAnalytics(_ context.Context, repo string, _ CommunityLabeler, _ int) (RecomputeResult, error) {
	b.inRecompute <- struct{}{}
	<-b.blockCh
	b.mu.Lock()
	b.recomputed = append(b.recomputed, repo)
	b.mu.Unlock()
	return RecomputeResult{}, nil
}

func TestWorker_SingleFlightPerRepo(t *testing.T) {
	// Unbuffered tick channel so we control exactly when processOnce is triggered.
	tickC := make(chan time.Time)
	inRecompute := make(chan struct{}, 1)
	blockCh := make(chan struct{})

	bs := &blockingAnalyticsStore{
		fakeAnalyticsStore: &fakeAnalyticsStore{dirty: []string{"repo/x"}},
		inRecompute:        inRecompute,
		blockCh:            blockCh,
	}

	w := NewAnalyticsWorker(bs, nil, AnalyticsWorkerConfig{
		DebounceSecs: 10,
		tickC:        tickC,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go w.Run(ctx)

	// First tick: processOnce calls DirtyRepos then starts recompute("repo/x") in a goroutine.
	tickC <- time.Now()

	// Wait until we are inside RecomputeAnalytics (first call is in-flight).
	<-inRecompute

	// Second tick while first recompute is still blocked; single-flight must skip.
	tickC <- time.Now()
	// Give processOnce time to observe inflight and skip.
	time.Sleep(30 * time.Millisecond)

	// Unblock the first recompute.
	close(blockCh)

	require.Eventually(t, func() bool {
		return len(bs.recomputedRepos()) >= 1
	}, 2*time.Second, 10*time.Millisecond)

	// Exactly 1 call even though two ticks fired while the first was in-flight.
	require.Equal(t, 1, len(bs.recomputedRepos()))
}

// metricsStore returns a fixed RecomputeResult and optional error, and reports a
// fixed dirty list, so the metrics test can drive both outcomes deterministically.
type metricsStore struct {
	dirty []string
	res   RecomputeResult
	err   error
}

func (m *metricsStore) DirtyRepos(context.Context, int) ([]string, error) {
	return m.dirty, nil
}

func (m *metricsStore) RecomputeAnalytics(context.Context, string, CommunityLabeler, int) (RecomputeResult, error) {
	return m.res, m.err
}

func gatherGauge(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf.Metric[0].GetGauge().GetValue()
		}
	}
	t.Fatalf("gauge %s not found", name)
	return 0
}

func gatherRunCounter(t *testing.T, reg *prometheus.Registry, result string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != "code_graph_analytics_runs_total" {
			continue
		}
		for _, m := range mf.Metric {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "result" && lp.GetValue() == result {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	t.Fatalf("counter code_graph_analytics_runs_total{result=%q} not found", result)
	return 0
}

func gatherHistogramCount(t *testing.T, reg *prometheus.Registry, name string) uint64 {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf.Metric[0].GetHistogram().GetSampleCount()
		}
	}
	t.Fatalf("histogram %s not found", name)
	return 0
}

func TestAnalyticsMetrics_RegisteredAtZero(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = NewAnalyticsMetrics(reg)

	mfs, err := reg.Gather()
	require.NoError(t, err)
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"code_graph_analytics_runs_total",
		"code_graph_analytics_duration_seconds",
		"code_graph_analytics_in_flight",
		"code_graph_analytics_dirty_repos",
	} {
		require.Truef(t, names[want], "metric family %s not registered at construction", want)
	}
	// Both result labels pre-initialized at zero.
	require.Equal(t, 0.0, gatherRunCounter(t, reg, analyticsResultSuccess))
	require.Equal(t, 0.0, gatherRunCounter(t, reg, analyticsResultError))
}

func TestWorker_MetricsSuccessAndError(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		tickC := make(chan time.Time, 1)
		store := &metricsStore{dirty: []string{"repo/a"}, res: RecomputeResult{Entities: 3, Communities: 1}}
		w := NewAnalyticsWorker(store, nil, AnalyticsWorkerConfig{Registerer: reg, tickC: tickC})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go w.Run(ctx)

		tickC <- time.Now()
		require.Eventually(t, func() bool {
			return gatherRunCounter(t, reg, analyticsResultSuccess) == 1
		}, 2*time.Second, 10*time.Millisecond)

		require.Equal(t, 0.0, gatherRunCounter(t, reg, analyticsResultError))
		require.Equal(t, 1.0, gatherGauge(t, reg, "code_graph_analytics_dirty_repos"))
		require.Equal(t, 0.0, gatherGauge(t, reg, "code_graph_analytics_in_flight"))
		require.Equal(t, uint64(1), gatherHistogramCount(t, reg, "code_graph_analytics_duration_seconds"))
	})

	t.Run("error", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		tickC := make(chan time.Time, 1)
		store := &metricsStore{dirty: []string{"repo/a"}, err: errors.New("boom")}
		w := NewAnalyticsWorker(store, nil, AnalyticsWorkerConfig{Registerer: reg, tickC: tickC})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go w.Run(ctx)

		tickC <- time.Now()
		require.Eventually(t, func() bool {
			return gatherRunCounter(t, reg, analyticsResultError) == 1
		}, 2*time.Second, 10*time.Millisecond)

		require.Equal(t, 0.0, gatherRunCounter(t, reg, analyticsResultSuccess))
		require.Equal(t, uint64(1), gatherHistogramCount(t, reg, "code_graph_analytics_duration_seconds"))
	})
}
