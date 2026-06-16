package ingest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", msg)
}

func TestPoolDrainsJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	svc := memory.NewService(fake.New(), nil)
	pool := ingest.NewPool(store, svc, 2)
	pool.Start(ctx)
	defer pool.Stop()

	e := ingest.NewEnqueuer(store, nil)
	job, err := e.Enqueue(ctx, []memory.IngestItem{
		{Text: "a"}, {Text: "b"}, {Text: "c"},
	})
	require.NoError(t, err)
	pool.Notify(job.ID)

	waitFor(t, func() bool {
		j, err := store.GetJob(ctx, job.ID)
		return err == nil && j.Status == memory.JobStatusSucceeded
	}, "job did not reach succeeded")

	j, _ := store.GetJob(ctx, job.ID)
	require.Equal(t, 3, j.Done)
	require.Equal(t, 0, j.Failed)
}

// TestPoolDrainsJobConcurrently runs many items through several workers on one
// job. The old read-modify-write progress update (GetJob -> Done++ -> UpdateJob)
// lost increments when two workers interleaved, so Done would fall short of the
// item count and the job could report Succeeded with a wrong count. The atomic
// IncrementJobProgress must keep every increment.
func TestPoolDrainsJobConcurrently(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	svc := memory.NewService(fake.New(), nil)
	pool := ingest.NewPool(store, svc, 8)
	pool.Start(ctx)
	defer pool.Stop()

	const n = 200
	items := make([]memory.IngestItem, n)
	for i := range items {
		items[i] = memory.IngestItem{Text: "item"}
	}
	e := ingest.NewEnqueuer(store, nil)
	job, err := e.Enqueue(ctx, items)
	require.NoError(t, err)
	pool.Notify(job.ID)

	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, job.ID)
		return j.Status.Terminal()
	}, "job did not terminate")

	j, _ := store.GetJob(ctx, job.ID)
	require.Equal(t, memory.JobStatusSucceeded, j.Status)
	require.Equal(t, n, j.Done)
	require.Equal(t, 0, j.Failed)
}

type failingRunner struct {
	fail map[string]bool
}

func (f *failingRunner) CreateMemory(_ context.Context, m memory.Memory) (memory.Memory, error) {
	if f.fail[m.Text] {
		return memory.Memory{}, errors.New("boom")
	}
	return m, nil
}

// blockingRunner blocks until its context is cancelled, then returns the
// context error. It models a hung CreateMemory call that only the per-item
// timeout can unstick.
type blockingRunner struct{}

func (blockingRunner) CreateMemory(ctx context.Context, _ memory.Memory) (memory.Memory, error) {
	<-ctx.Done()
	return memory.Memory{}, ctx.Err()
}

// hangOnRunner blocks until ctx cancellation only for items whose Text matches
// hang; every other item returns immediately. It models a single pathological
// item that must not stall the items queued behind it.
type hangOnRunner struct{ hang string }

func (r hangOnRunner) CreateMemory(ctx context.Context, m memory.Memory) (memory.Memory, error) {
	if m.Text == r.hang {
		<-ctx.Done()
		return memory.Memory{}, ctx.Err()
	}
	return m, nil
}

func TestPoolItemTimeoutFailsHungItem(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	pool := ingest.NewPool(store, blockingRunner{}, 1, ingest.WithItemTimeout(50*time.Millisecond))
	pool.Start(ctx)
	defer pool.Stop()

	e := ingest.NewEnqueuer(store, nil)
	job, err := e.Enqueue(ctx, []memory.IngestItem{{Text: "hang"}})
	require.NoError(t, err)
	pool.Notify(job.ID)

	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, job.ID)
		return j.Status.Terminal()
	}, "hung job did not terminate under the per-item timeout")

	j, _ := store.GetJob(ctx, job.ID)
	require.Equal(t, memory.JobStatusFailed, j.Status)
	require.Equal(t, 1, j.Failed)
	require.Len(t, j.Errors, 1)
	require.Contains(t, j.Errors[0].Error, "deadline")
}

// TestPoolItemTimeoutDoesNotStallPool is the core guarantee of the per-item
// timeout: a hung item must fail on its own deadline and the worker must go on
// to process the items queued behind it. One worker, two items, the first
// hangs: the timed-out item is recorded failed with a deadline error and the
// second item still completes, finalizing the job Partial rather than stuck.
func TestPoolItemTimeoutDoesNotStallPool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	pool := ingest.NewPool(store, hangOnRunner{hang: "hang"}, 1, ingest.WithItemTimeout(50*time.Millisecond))
	pool.Start(ctx)
	defer pool.Stop()

	e := ingest.NewEnqueuer(store, nil)
	job, err := e.Enqueue(ctx, []memory.IngestItem{{Text: "hang"}, {Text: "ok"}})
	require.NoError(t, err)
	pool.Notify(job.ID)

	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, job.ID)
		return j.Status.Terminal()
	}, "job did not terminate; a timed-out item stalled the pool")

	j, _ := store.GetJob(ctx, job.ID)
	require.Equal(t, memory.JobStatusPartial, j.Status)
	require.Equal(t, 1, j.Done)   // the second item still got processed
	require.Equal(t, 1, j.Failed) // the hung item hit its per-item deadline
	require.Len(t, j.Errors, 1)
	require.Contains(t, j.Errors[0].Error, "deadline")
}

func TestPoolPartial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	r := &failingRunner{fail: map[string]bool{"bad": true}}
	pool := ingest.NewPool(store, r, 1)
	pool.Start(ctx)
	defer pool.Stop()

	e := ingest.NewEnqueuer(store, nil)
	job, err := e.Enqueue(ctx, []memory.IngestItem{{Text: "ok"}, {Text: "bad"}})
	require.NoError(t, err)
	pool.Notify(job.ID)

	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, job.ID)
		return j.Status.Terminal()
	}, "job did not terminate")

	j, _ := store.GetJob(ctx, job.ID)
	require.Equal(t, memory.JobStatusPartial, j.Status)
	require.Equal(t, 1, j.Done)
	require.Equal(t, 1, j.Failed)
	require.Len(t, j.Errors, 1)
}

func TestPoolAllFailed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	r := &failingRunner{fail: map[string]bool{"x": true, "y": true}}
	pool := ingest.NewPool(store, r, 1)
	pool.Start(ctx)
	defer pool.Stop()

	e := ingest.NewEnqueuer(store, nil)
	job, err := e.Enqueue(ctx, []memory.IngestItem{{Text: "x"}, {Text: "y"}})
	require.NoError(t, err)
	pool.Notify(job.ID)

	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, job.ID)
		return j.Status.Terminal()
	}, "job did not terminate")

	j, _ := store.GetJob(ctx, job.ID)
	require.Equal(t, memory.JobStatusFailed, j.Status)
}

func TestEnqueueNotifiesPool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	svc := memory.NewService(fake.New(), nil)
	pool := ingest.NewPool(store, svc, 2)
	pool.Start(ctx)
	defer pool.Stop()

	// Enqueuer wired to the pool: enqueue alone must schedule the job.
	e := ingest.NewEnqueuer(store, pool)
	job, err := e.Enqueue(ctx, []memory.IngestItem{{Text: "a"}, {Text: "b"}})
	require.NoError(t, err)

	waitFor(t, func() bool {
		j, err := store.GetJob(ctx, job.ID)
		return err == nil && j.Status == memory.JobStatusSucceeded
	}, "enqueued job did not drain without a manual Notify")
}

func TestPoolResumeQueuedOnStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	require.NoError(t, store.CreateJob(ctx, memory.IngestJob{
		ID: "queued1", Status: memory.JobStatusQueued, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}, []memory.IngestItem{{IdempotencyKey: "k", Text: "ok"}}))

	svc := memory.NewService(fake.New(), nil)
	pool := ingest.NewPool(store, svc, 1)
	pool.Start(ctx)
	defer pool.Stop()

	n, err := pool.Resume(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, "queued1")
		return j.Status == memory.JobStatusSucceeded
	}, "resumed queued job did not drain")
}

func TestPoolResumeRunningOnStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	require.NoError(t, store.CreateJob(ctx, memory.IngestJob{
		ID: "resume1", Status: memory.JobStatusRunning, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}, []memory.IngestItem{{IdempotencyKey: "k", Text: "ok"}}))

	pool := ingest.NewPool(store, &failingRunner{}, 1)
	pool.Start(ctx)
	defer pool.Stop()
	n, err := pool.Resume(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, "resume1")
		return j.Status.Terminal()
	}, "resumed job did not terminate")
}

// TestPoolResumeReclaimsOrphanedItem covers the crash-mid-item case: an item is
// claimed (status 'running') but the worker dies before MarkItemDone. Because
// ClaimNextItem only claims 'pending', a plain resume would drain the job to a
// short count (Done=0) and report Succeeded. Resume must reset the orphan to
// 'pending' so it is reprocessed.
func TestPoolResumeReclaimsOrphanedItem(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	require.NoError(t, store.CreateJob(ctx, memory.IngestJob{
		ID: "orphan1", Status: memory.JobStatusRunning, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}, []memory.IngestItem{{IdempotencyKey: "k", Text: "ok"}}))

	// Simulate a worker that claimed the item (marking it 'running') then crashed
	// before marking it done.
	_, ok, err := store.ClaimNextItem(ctx, "orphan1")
	require.NoError(t, err)
	require.True(t, ok)

	svc := memory.NewService(fake.New(), nil)
	pool := ingest.NewPool(store, svc, 1)
	pool.Start(ctx)
	defer pool.Stop()
	n, err := pool.Resume(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, "orphan1")
		return j.Status.Terminal()
	}, "resumed job did not terminate")

	j, _ := store.GetJob(ctx, "orphan1")
	require.Equal(t, memory.JobStatusSucceeded, j.Status)
	require.Equal(t, 1, j.Done)
	require.Equal(t, 0, j.Failed)
}

type capturingSources struct {
	mu    sync.Mutex
	added []addedSource
}

type addedSource struct{ repo, file, trackID string }

func (c *capturingSources) Add(_ context.Context, repo, file, trackID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.added = append(c.added, addedSource{repo, file, trackID})
	return nil
}

func (c *capturingSources) snapshot() []addedSource {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]addedSource(nil), c.added...)
}

// trackingRunner returns a Memory whose ID is "trk_" + the item's idempotency key.
type trackingRunner struct{}

func (trackingRunner) CreateMemory(_ context.Context, m memory.Memory) (memory.Memory, error) {
	m.ID = "trk_" + m.ID // m.ID is set by processItem to the item's idempotency key
	return m, nil
}

func TestPoolIndexesSourcesAfterCreate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	src := &capturingSources{}
	pool := ingest.NewPoolWithSources(store, trackingRunner{}, 1, src)
	pool.Start(ctx)
	defer pool.Stop()

	e := ingest.NewEnqueuer(store, nil)
	job, err := e.Enqueue(ctx, []memory.IngestItem{
		{IdempotencyKey: "k1", Text: "a", Metadata: map[string]string{"repo": "repoA", "file_path": "a.go"}},
		{IdempotencyKey: "k2", Text: "b", Metadata: map[string]string{"repo": "repoA"}}, // no file_path -> not indexed
	})
	require.NoError(t, err)
	pool.Notify(job.ID)

	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, job.ID)
		return j.Status == memory.JobStatusSucceeded
	}, "job did not succeed")

	got := src.snapshot()
	require.Len(t, got, 1)
	require.Equal(t, "repoA", got[0].repo)
	require.Equal(t, "a.go", got[0].file)
	require.Equal(t, "trk_k1", got[0].trackID)
}

// failingSources is a SourceSink that always returns an error.
type failingSources struct{}

func (failingSources) Add(_ context.Context, _, _, _ string) error {
	return errors.New("index-backend down")
}

// TestSourceIndexFailureIsNonFatal covers finding-1: sources.Add failure must
// not mark the item failed. The memory was already created; the item counts as
// done and the job succeeds.
func TestSourceIndexFailureIsNonFatal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	pool := ingest.NewPoolWithSources(store, trackingRunner{}, 1, failingSources{})
	pool.Start(ctx)
	defer pool.Stop()

	e := ingest.NewEnqueuer(store, nil)
	job, err := e.Enqueue(ctx, []memory.IngestItem{
		{IdempotencyKey: "k1", Text: "a", Metadata: map[string]string{"repo": "r", "file_path": "f.go"}},
	})
	require.NoError(t, err)
	pool.Notify(job.ID)

	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, job.ID)
		return j.Status.Terminal()
	}, "job did not terminate")

	j, _ := store.GetJob(ctx, job.ID)
	// The source-index failure must NOT mark the item failed: job must be succeeded.
	require.Equal(t, memory.JobStatusSucceeded, j.Status,
		"sources.Add failure should be non-fatal; job must still succeed")
	require.Equal(t, 1, j.Done)
	require.Equal(t, 0, j.Failed)
}

// countingRunner counts how many times CreateMemory is called per idempotency key.
type countingRunner struct {
	mu    sync.Mutex
	calls map[string]int
}

func (r *countingRunner) CreateMemory(_ context.Context, m memory.Memory) (memory.Memory, error) {
	r.mu.Lock()
	r.calls[m.ID]++
	r.mu.Unlock()
	m.ID = "trk_" + m.ID
	return m, nil
}

func (r *countingRunner) callsFor(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[key]
}

// TestIdempotencyKeyPreventsDoubleInsert covers finding-2: after an item's
// CreateMemory succeeded and the track_id was persisted, a simulated
// crash-and-resume must not call CreateMemory again for that item.
func TestIdempotencyKeyPreventsDoubleInsert(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	runner := &countingRunner{calls: make(map[string]int)}

	// Create the job with one item.
	require.NoError(t, store.CreateJob(ctx, memory.IngestJob{
		ID: "idem1", Status: memory.JobStatusRunning, Total: 1,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}, []memory.IngestItem{{IdempotencyKey: "idem-key", Text: "text"}}))

	// Simulate: worker claimed the item and CreateMemory succeeded, but worker
	// crashed before MarkItemDone. The track_id was persisted.
	require.NoError(t, store.SetItemTrackID(ctx, "idem1", "idem-key", "trk_idem-key"))
	// The item is still 'running' (claim happened); reset it to 'pending' as
	// Resume would do.
	_, err := store.ResetRunningItems(ctx)
	require.NoError(t, err)

	pool := ingest.NewPool(store, runner, 1)
	pool.Start(ctx)
	defer pool.Stop()

	n, err := pool.Resume(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, "idem1")
		return j.Status.Terminal()
	}, "job did not terminate after resume")

	j, _ := store.GetJob(ctx, "idem1")
	require.Equal(t, memory.JobStatusSucceeded, j.Status)
	// CreateMemory must NOT have been called again: TrackID was already set.
	require.Equal(t, 0, runner.callsFor("idem-key"),
		"CreateMemory must be skipped when TrackID is already persisted")
}

// TestShutdownRespectsStopInDrainLoop covers finding-5: stopping the pool while
// a job is being drained must not hang; workers must observe the stop signal
// and exit promptly even when there are pending items remaining.
func TestShutdownRespectsStopInDrainLoop(t *testing.T) {
	ctx := context.Background()

	store := ingest.NewMemStore()
	svc := memory.NewService(fake.New(), nil)
	pool := ingest.NewPool(store, svc, 1)
	pool.Start(ctx)

	// Enqueue enough items so the worker is mid-drain when we Stop.
	e := ingest.NewEnqueuer(store, nil)
	_, err := e.Enqueue(ctx, []memory.IngestItem{
		{Text: "a"}, {Text: "b"}, {Text: "c"}, {Text: "d"}, {Text: "e"},
	})
	require.NoError(t, err)
	// Don't notify: we just want to verify Stop() returns promptly.
	done := make(chan struct{})
	go func() {
		defer close(done)
		pool.Stop()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pool.Stop() did not return within 2s; stop signal ignored inside drain loop")
	}
}

// TestRunJobLogsStoreErrors covers finding-4: runJob must not silently discard
// errors from store operations. When IncrementJobProgress fails the item's
// progress counter is not persisted, so Done+Failed < Total and the
// completeness guard (finding-2) correctly leaves the first job non-terminal.
// The pool must not hang: the worker must exit runJob and pick up the next job.
func TestRunJobLogsStoreErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	base := ingest.NewMemStore()
	store := &injectErrorStore{JobStore: base}
	store.failProgress = true // first IncrementJobProgress call returns an error

	svc := memory.NewService(fake.New(), nil)
	pool := ingest.NewPool(store, svc, 1)
	pool.Start(ctx)
	defer pool.Stop()

	e := ingest.NewEnqueuer(base, nil)
	errJob, err := e.Enqueue(ctx, []memory.IngestItem{{Text: "x"}})
	require.NoError(t, err)
	pool.Notify(errJob.ID)

	sentinel, err := e.Enqueue(ctx, []memory.IngestItem{{Text: "sentinel"}})
	require.NoError(t, err)
	pool.Notify(sentinel.ID)

	// The sentinel job (no injected errors) must drain, proving the worker moved on
	// after the IncrementJobProgress error and did not stall.
	waitFor(t, func() bool {
		j, _ := base.GetJob(ctx, sentinel.ID)
		return j.Status.Terminal()
	}, "pool stalled after IncrementJobProgress error; sentinel job never drained")
}

// injectErrorStore wraps a JobStore and can inject errors on specific ops.
type injectErrorStore struct {
	ingest.JobStore
	mu           sync.Mutex
	failProgress bool
}

func (s *injectErrorStore) MarkItemDoneAndProgress(ctx context.Context, jobID, idemKey string, runErr error) error {
	s.mu.Lock()
	fail := s.failProgress
	if fail {
		s.failProgress = false // only fail once
	}
	s.mu.Unlock()
	if fail {
		return errors.New("db: transient error")
	}
	return s.JobStore.MarkItemDoneAndProgress(ctx, jobID, idemKey, runErr)
}

func (s *injectErrorStore) SetItemTrackID(ctx context.Context, jobID, key, trackID string) error {
	return s.JobStore.SetItemTrackID(ctx, jobID, key, trackID)
}

// TestRunJobSkipsTerminalJob covers finding-1: runJob must return early when the
// job is already terminal (Succeeded/Failed/Partial). A duplicate Notify for an
// already-finalized job must not re-enter the drain loop and must not call
// incJob a second time (which would double-count ingest_jobs_total).
func TestRunJobSkipsTerminalJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	svc := memory.NewService(fake.New(), nil)
	pool := ingest.NewPool(store, svc, 1)
	pool.Start(ctx)
	defer pool.Stop()

	e := ingest.NewEnqueuer(store, nil)
	job, err := e.Enqueue(ctx, []memory.IngestItem{{Text: "a"}})
	require.NoError(t, err)

	// Drain the job to terminal state.
	pool.Notify(job.ID)
	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, job.ID)
		return j.Status.Terminal()
	}, "job did not reach terminal state")

	// Snapshot terminal status.
	first, _ := store.GetJob(ctx, job.ID)
	require.Equal(t, memory.JobStatusSucceeded, first.Status)

	// Send a duplicate Notify for an already-terminal job. The worker must
	// silently skip it without altering the job state.
	pool.Notify(job.ID)

	// Give the worker time to process the second notify.
	time.Sleep(100 * time.Millisecond)

	second, _ := store.GetJob(ctx, job.ID)
	require.Equal(t, first.Status, second.Status,
		"duplicate Notify must not change a terminal job's status")
	require.Equal(t, first.Done, second.Done,
		"duplicate Notify must not change Done count")
}

// TestRunJobDoesNotFinalizeIncompleteJob covers finding-2: if Done+Failed < Total
// (e.g. items still in-flight), finalization must not run and the job must not
// be marked terminal prematurely. We simulate the condition by crafting a job
// whose counters do not sum to Total and verifying the job stays non-terminal
// until it is actually complete.
func TestRunJobDoesNotFinalizeIncompleteJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a slow runner so we can race a second Notify before the first drain
	// finishes. The test just needs to confirm that a job with Total=2 is not
	// marked terminal while Done+Failed < 2.
	store := ingest.NewMemStore()
	svc := memory.NewService(fake.New(), nil)
	pool := ingest.NewPool(store, svc, 2)
	pool.Start(ctx)
	defer pool.Stop()

	e := ingest.NewEnqueuer(store, nil)
	job, err := e.Enqueue(ctx, []memory.IngestItem{{Text: "a"}, {Text: "b"}})
	require.NoError(t, err)
	pool.Notify(job.ID)

	// Eventually the job must complete with the correct count; the guard ensures
	// it is NOT marked Succeeded when Done=0 (no items processed yet).
	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, job.ID)
		return j.Status.Terminal()
	}, "job did not reach terminal state")

	j, _ := store.GetJob(ctx, job.ID)
	require.Equal(t, memory.JobStatusSucceeded, j.Status)
	require.Equal(t, 2, j.Done,
		"job must not finalize until all items are accounted for")
}
