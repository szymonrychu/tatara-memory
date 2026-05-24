package ingest

import (
	"context"
	"sync"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

const maxErrors = 50

// itemRunner is the subset of memory.Service used by the Pool.
type itemRunner interface {
	CreateMemory(ctx context.Context, m memory.Memory) (memory.Memory, error)
}

// Pool is an async worker pool that processes queued ingest jobs.
type Pool struct {
	store   JobStore
	runner  itemRunner
	size    int
	notify  chan string
	stop    chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	started bool
}

// NewPool returns a Pool backed by the given store and runner with size worker goroutines.
func NewPool(store JobStore, runner itemRunner, size int) *Pool {
	if size < 1 {
		size = 1
	}
	return &Pool{
		store:  store,
		runner: runner,
		size:   size,
		notify: make(chan string, 256),
		stop:   make(chan struct{}),
	}
}

// Start launches the worker goroutines. Calling Start more than once is a no-op.
func (p *Pool) Start(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return
	}
	p.started = true
	for i := 0; i < p.size; i++ {
		p.wg.Add(1)
		go p.worker(ctx)
	}
}

// Stop signals all workers to exit and waits for them to finish.
func (p *Pool) Stop() {
	close(p.stop)
	p.wg.Wait()
}

// Notify queues the given job ID for processing. Drops silently if the channel is full.
func (p *Pool) Notify(jobID string) {
	select {
	case p.notify <- jobID:
	default:
	}
}

// Resume re-queues all jobs that were in the running state at startup.
func (p *Pool) Resume(ctx context.Context) error {
	ids, err := p.store.ListRunningJobs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		p.Notify(id)
	}
	return nil
}

func (p *Pool) worker(ctx context.Context) {
	defer p.wg.Done()
	for {
		select {
		case <-p.stop:
			return
		case <-ctx.Done():
			return
		case jobID := <-p.notify:
			p.runJob(ctx, jobID)
		}
	}
}

func (p *Pool) runJob(ctx context.Context, jobID string) {
	j, err := p.store.GetJob(ctx, jobID)
	if err != nil {
		return
	}
	if j.Status == memory.JobStatusQueued {
		j.Status = memory.JobStatusRunning
		j.UpdatedAt = time.Now()
		_ = p.store.UpdateJob(ctx, j)
	}
	for {
		item, ok, err := p.store.ClaimNextItem(ctx, jobID)
		if err != nil || !ok {
			break
		}
		runErr := p.processItem(ctx, item)
		_ = p.store.MarkItemDone(ctx, jobID, item.IdempotencyKey, runErr)

		cur, _ := p.store.GetJob(ctx, jobID)
		if runErr != nil {
			cur.Failed++
			if len(cur.Errors) < maxErrors {
				cur.Errors = append(cur.Errors, memory.IngestItemError{
					IdempotencyKey: item.IdempotencyKey,
					Error:          runErr.Error(),
				})
			}
		} else {
			cur.Done++
		}
		cur.UpdatedAt = time.Now()
		_ = p.store.UpdateJob(ctx, cur)
	}
	final, err := p.store.GetJob(ctx, jobID)
	if err != nil {
		return
	}
	switch {
	case final.Failed == 0:
		final.Status = memory.JobStatusSucceeded
	case final.Done == 0:
		final.Status = memory.JobStatusFailed
	default:
		final.Status = memory.JobStatusPartial
	}
	final.UpdatedAt = time.Now()
	_ = p.store.UpdateJob(ctx, final)
}

func (p *Pool) processItem(ctx context.Context, it memory.IngestItem) error {
	_, err := p.runner.CreateMemory(ctx, memory.Memory{
		ID:       it.IdempotencyKey,
		Text:     it.Text,
		Metadata: it.Metadata,
	})
	return err
}
