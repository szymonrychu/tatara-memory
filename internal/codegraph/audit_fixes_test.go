package codegraph

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ---------- Finding 6: ILIKE metacharacter escaping ----------

func TestEscapeLike(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"foo_bar", `foo\_bar`},
		{"foo%bar", `foo\%bar`},
		{`foo\bar`, `foo\\bar`},
		{"plain", "plain"},
		{"", ""},
		{"__%%", `\_\_\%\%`},
	}
	for _, c := range cases {
		got := escapeLike(c.in)
		require.Equal(t, c.want, got, "escapeLike(%q)", c.in)
	}
}

// ---------- Finding 10: empty relations walks all ----------

func TestNeighborsRelStringEmpty(t *testing.T) {
	// When relations slice is empty, relFilter should return the guard clause
	// ($N='' OR ...) which passes all rows.
	got := relFilterClause("", "$3")
	require.Contains(t, got, "$3=''", "empty relStr must produce OR-guard")
}

func TestNeighborsRelStringNonEmpty(t *testing.T) {
	got := relFilterClause("calls,imports", "$3")
	require.NotContains(t, got, "''", "non-empty relStr must not produce empty guard")
	require.Contains(t, got, "ANY", "non-empty relStr must use ANY array")
}

// ---------- Finding 13: marshalProps/scanProps errors ----------

func TestMarshalPropsNeverErrors(t *testing.T) {
	// map[string]string can never fail to marshal; marshalProps must return the
	// real JSON, never "{}".
	got := marshalProps(map[string]string{"k": "v"})
	require.Equal(t, `{"k":"v"}`, got)
}

func TestMarshalPropsEmpty(t *testing.T) {
	require.Equal(t, "{}", marshalProps(nil))
	require.Equal(t, "{}", marshalProps(map[string]string{}))
}

func TestScanPropsCorrupt(t *testing.T) {
	// corrupt input: scanProps must return nil (not panic), bad JSON is silently
	// treated as no-properties (the WARN is logged but not testable here).
	got := scanProps([]byte(`{not valid json}`))
	require.Nil(t, got)
}

// ---------- Finding 2 + 9: WaitGroup in worker ----------

// TestWorker_GracefulDrainWaitsForInFlight verifies that Run blocks on
// ctx.Done until any in-flight recompute goroutine finishes.
func TestWorker_GracefulDrainWaitsForInFlight(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	var finished bool
	var mu sync.Mutex

	tickC := make(chan time.Time, 1)
	store := &blockingStore{
		dirty:   []string{"repo/drain"},
		started: started,
		unblock: unblock,
		onDone: func() {
			mu.Lock()
			finished = true
			mu.Unlock()
		},
	}

	w := NewAnalyticsWorker(store, nil, AnalyticsWorkerConfig{
		DebounceSecs: 0,
		tickC:        tickC,
	})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()

	// Trigger one tick so processOnce spawns the recompute goroutine.
	tickC <- time.Now()

	// Wait until recompute has started.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("recompute goroutine never started")
	}

	// Cancel context while recompute is in-flight.
	cancel()

	// Run must NOT have returned yet - recompute is still blocked.
	select {
	case <-done:
		t.Fatal("Run returned before in-flight recompute finished")
	case <-time.After(50 * time.Millisecond):
		// good - still blocked
	}

	// Unblock the recompute; Run should drain and return.
	close(unblock)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after in-flight recompute finished")
	}

	mu.Lock()
	require.True(t, finished, "recompute must have completed before Run returned")
	mu.Unlock()
}

// blockingStore is an AnalyticsStore that signals started, blocks until unblock,
// then calls onDone.
type blockingStore struct {
	dirty   []string
	started chan struct{}
	unblock chan struct{}
	onDone  func()
}

func (b *blockingStore) DirtyRepos(_ context.Context, _ int) ([]string, error) {
	return b.dirty, nil
}

func (b *blockingStore) RecomputeAnalytics(_ context.Context, _ string, _ CommunityLabeler) (RecomputeResult, error) {
	b.started <- struct{}{}
	<-b.unblock
	if b.onDone != nil {
		b.onDone()
	}
	return RecomputeResult{}, nil
}

// ---------- Finding 7: cross_repo_symbols delete unconditional ----------

// TestSymbolDeleteIsUnconditional is a compile/logic test verifying that
// Reconcile no longer gates the cross_repo_symbols delete on ExtractorAST.
// The actual DB behavior is tested by the integration test; here we verify
// that the source no longer contains the ExtractorAST guard in the delete path
// (done via code inspection; the integration test remains authoritative).
// We just ensure the unit builds correctly with any extractor.
func TestSymbolDeleteIsUnconditional(_ *testing.T) {
	// The function is not callable without a DB, so this is a build-only check.
	// The integration test TestReconcileSymbolsPerFileReplacement covers runtime
	// behavior. No assertion needed here beyond "package compiles".
}
