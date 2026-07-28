package codegraph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// deadlineProbeStore records the deadline (if any) on the context it is handed
// and, when block is true, waits for that context to be cancelled - the shape of
// a recompute wedged on an unresponsive dependency.
type deadlineProbeStore struct {
	fakeAnalyticsStore
	block    bool
	deadline chan time.Time // buffered; receives zero time when ctx has no deadline
}

func (s *deadlineProbeStore) RecomputeAnalytics(ctx context.Context, _ string, _ CommunityLabeler, _ int) (RecomputeResult, error) {
	dl, _ := ctx.Deadline()
	select {
	case s.deadline <- dl:
	default:
	}
	if !s.block {
		return RecomputeResult{}, nil
	}
	<-ctx.Done()
	return RecomputeResult{}, ctx.Err()
}

// TestWorker_RecomputeTimeoutBoundsARun is the regression test for the second
// half of tatara-memory#89: the worker used to hand RecomputeAnalytics the
// process-lifetime context (cmd/tatara-memory/app.go's
// context.WithCancel(context.Background())), so a single wedged run held its
// database work open for as long as the process lived - the observed
// transaction age grew 1800s per 1800s of wall clock for 5.5h. A per-run
// deadline puts a ceiling on that.
func TestWorker_RecomputeTimeoutBoundsARun(t *testing.T) {
	tickC := make(chan time.Time, 1)
	reg := prometheus.NewRegistry()
	store := &deadlineProbeStore{
		fakeAnalyticsStore: fakeAnalyticsStore{dirty: []string{"repo/a"}},
		block:              true,
		deadline:           make(chan time.Time, 1),
	}

	w := NewAnalyticsWorker(store, nil, AnalyticsWorkerConfig{
		tickC:            tickC,
		Registerer:       reg,
		RecomputeTimeout: 100 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()

	start := time.Now()
	tickC <- time.Now()

	select {
	case dl := <-store.deadline:
		require.False(t, dl.IsZero(), "RecomputeAnalytics was handed a context with no deadline")
		require.WithinDuration(t, start.Add(100*time.Millisecond), dl, time.Second)
	case <-time.After(2 * time.Second):
		t.Fatal("recompute never started")
	}

	// The run must be cut off by its own deadline, and be counted as such.
	require.Eventually(t, func() bool {
		return counterValue(t, reg, "code_graph_analytics_runs_total", "timeout") == 1
	}, 3*time.Second, 10*time.Millisecond,
		"a recompute that outran its deadline must be counted as result=timeout")
	require.Less(t, time.Since(start), 3*time.Second, "the run must end at its deadline, not run unbounded")

	cancel()
	<-done
}

// TestWorker_NoRecomputeTimeoutWhenUnset documents the opt-out: a zero
// RecomputeTimeout leaves the caller's context untouched (0 disables, matching
// ingest.WithItemTimeout).
func TestWorker_NoRecomputeTimeoutWhenUnset(t *testing.T) {
	tickC := make(chan time.Time, 1)
	store := &deadlineProbeStore{
		fakeAnalyticsStore: fakeAnalyticsStore{dirty: []string{"repo/a"}},
		deadline:           make(chan time.Time, 1),
	}

	w := NewAnalyticsWorker(store, nil, AnalyticsWorkerConfig{tickC: tickC})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()

	tickC <- time.Now()
	select {
	case dl := <-store.deadline:
		require.True(t, dl.IsZero(), "no RecomputeTimeout configured, so the context must carry no deadline")
	case <-time.After(2 * time.Second):
		t.Fatal("recompute never started")
	}

	cancel()
	<-done
}

func counterValue(t *testing.T, reg *prometheus.Registry, name, result string) float64 {
	t.Helper()
	fams, err := reg.Gather()
	require.NoError(t, err)
	for _, f := range fams {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "result" && l.GetValue() == result {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

// TestWorker_DeadlineExceededIsNotCountedAsAGenericError guards the
// classification itself: a timeout must be distinguishable from a real store
// error in code_graph_analytics_runs_total, otherwise the new bound is
// invisible on the dashboard.
func TestWorker_DeadlineExceededIsNotCountedAsAGenericError(t *testing.T) {
	require.True(t, errors.Is(context.DeadlineExceeded, context.DeadlineExceeded))
	require.Equal(t, "timeout", analyticsResultTimeout)
	require.NotEqual(t, analyticsResultError, analyticsResultTimeout)
}
