package codegraph

import (
	"context"
	"sync"
	"testing"
	"time"

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

func (f *fakeAnalyticsStore) RecomputeAnalytics(_ context.Context, repo string, _ CommunityLabeler) error {
	if f.blockCh != nil {
		<-f.blockCh
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recomputed = append(f.recomputed, repo)
	return nil
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

func (b *blockingAnalyticsStore) RecomputeAnalytics(_ context.Context, repo string, _ CommunityLabeler) error {
	b.inRecompute <- struct{}{}
	<-b.blockCh
	b.mu.Lock()
	b.recomputed = append(b.recomputed, repo)
	b.mu.Unlock()
	return nil
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
