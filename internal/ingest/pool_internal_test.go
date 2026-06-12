package ingest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type slowRunner struct{}

func (slowRunner) CreateMemory(_ context.Context, m memory.Memory) (memory.Memory, error) {
	time.Sleep(2 * time.Millisecond)
	return m, nil
}

type hangRunner struct{ started chan struct{} }

func (h hangRunner) CreateMemory(ctx context.Context, _ memory.Memory) (memory.Memory, error) {
	close(h.started)
	<-ctx.Done()
	return memory.Memory{}, ctx.Err()
}

// A hung CreateMemory must not block the worker forever: with an item timeout
// the context is cancelled, the item is marked failed, and the job finishes.
// Without the deadline this test would hang until the package timeout.
func TestItemTimeoutFreesHungWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := NewMemStore()
	if err := store.CreateJob(ctx, memory.IngestJob{
		ID: "j1", Status: memory.JobStatusQueued, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}, []memory.IngestItem{{IdempotencyKey: "j1-k", Text: "t"}}); err != nil {
		t.Fatal(err)
	}

	runner := hangRunner{started: make(chan struct{})}
	p := newPool(store, runner, 1, 1, nil)
	p.SetItemTimeout(50 * time.Millisecond)
	p.Start(ctx)
	defer p.Stop()
	p.Notify("j1")

	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never picked up the item")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		j, _ := store.GetJob(ctx, "j1")
		if j.Status == memory.JobStatusFailed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not fail after item timeout; status=%s (worker stayed blocked)", j.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Resume must schedule every unfinished job even when there are far more than
// the notify buffer holds: dropping any leaves a job stuck queued forever,
// defeating crash recovery. A single slow worker behind a buffer of 1 forces
// the overflow that a lossy send would silently drop.
func TestResumeSchedulesAllDespiteSmallBuffer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := NewMemStore()
	const n = 20
	id := func(i int) string { return fmt.Sprintf("job%02d", i) }
	for i := 0; i < n; i++ {
		if err := store.CreateJob(ctx, memory.IngestJob{
			ID: id(i), Status: memory.JobStatusQueued, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}, []memory.IngestItem{{IdempotencyKey: id(i) + "-k", Text: "t"}}); err != nil {
			t.Fatal(err)
		}
	}

	p := newPool(store, slowRunner{}, 1, 1, nil)
	p.Start(ctx)
	defer p.Stop()

	scheduled, err := p.Resume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if scheduled != n {
		t.Fatalf("Resume reported %d scheduled, want %d", scheduled, n)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		done := 0
		for i := 0; i < n; i++ {
			j, _ := store.GetJob(ctx, id(i))
			if j.Status == memory.JobStatusSucceeded {
				done++
			}
		}
		if done == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d jobs drained; Resume dropped jobs on a full buffer", done, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
