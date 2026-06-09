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
