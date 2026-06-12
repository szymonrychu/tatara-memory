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
