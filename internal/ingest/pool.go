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

// SourceSink records that a track_id was produced from a repo/file. The memory
// SourceStore satisfies it. May be nil (indexing disabled).
type SourceSink interface {
	Add(ctx context.Context, repo, filePath, trackID string) error
}

// Pool is an async worker pool that processes queued ingest jobs.
type Pool struct {
	store   JobStore
	runner  itemRunner
	size    int
	sources SourceSink
	notify  chan string
	stop    chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	started bool
}

// NewPool returns a Pool backed by the given store and runner with size worker goroutines.
func NewPool(store JobStore, runner itemRunner, size int) *Pool {
	return newPool(store, runner, size, 256, nil)
}

// NewPoolWithSources is NewPool plus a sink that indexes (repo, file_path,
// track_id) after each successful CreateMemory. sources may be nil.
func NewPoolWithSources(store JobStore, runner itemRunner, size int, sources SourceSink) *Pool {
	return newPool(store, runner, size, 256, sources)
}

func newPool(store JobStore, runner itemRunner, size, buf int, sources SourceSink) *Pool {
	if size < 1 {
		size = 1
	}
	if buf < 1 {
		buf = 1
	}
	return &Pool{
		store:   store,
		runner:  runner,
		size:    size,
		sources: sources,
		notify:  make(chan string, buf),
		stop:    make(chan struct{}),
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

// Resume re-queues every unfinished (queued or running) job found at startup
// and returns how many it scheduled. This recovers jobs that were enqueued but
// never notified (or whose notify was dropped) before a restart.
func (p *Pool) Resume(ctx context.Context) (int, error) {
	ids, err := p.store.ListUnfinishedJobs(ctx)
	if err != nil {
		return 0, err
	}
	for i, id := range ids {
		select {
		case p.notify <- id:
		case <-p.stop:
			return i, nil
		case <-ctx.Done():
			return i, ctx.Err()
		}
	}
	return len(ids), nil
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

		var itemErr *memory.IngestItemError
		if runErr != nil {
			itemErr = &memory.IngestItemError{
				IdempotencyKey: item.IdempotencyKey,
				Error:          runErr.Error(),
			}
		}
		_ = p.store.IncrementJobProgress(ctx, jobID, itemErr)
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
	created, err := p.runner.CreateMemory(ctx, memory.Memory{
		ID:       it.IdempotencyKey,
		Text:     it.Text,
		Metadata: it.Metadata,
	})
	if err != nil {
		return err
	}
	if p.sources != nil {
		repo := it.Metadata["repo"]
		file := it.Metadata["file_path"]
		if repo != "" && file != "" && created.ID != "" {
			if err := p.sources.Add(ctx, repo, file, created.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
