package ingest_test

import (
	"context"
	"errors"
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
	svc := memory.NewService(fake.New())
	pool := ingest.NewPool(store, svc, 2)
	pool.Start(ctx)
	defer pool.Stop()

	e := ingest.NewEnqueuer(store)
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

	e := ingest.NewEnqueuer(store)
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

	e := ingest.NewEnqueuer(store)
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
	require.NoError(t, pool.Resume(ctx))

	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, "resume1")
		return j.Status.Terminal()
	}, "resumed job did not terminate")
}
