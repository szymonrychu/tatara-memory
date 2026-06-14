package ingest

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

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
	store       JobStore
	runner      itemRunner
	size        int
	sources     SourceSink
	itemTimeout time.Duration
	metrics     *metrics
	notify      chan string
	stop        chan struct{}
	wg          sync.WaitGroup
	mu          sync.Mutex
	started     bool
}

// Option configures a Pool at construction time.
type Option func(*Pool)

// WithItemTimeout bounds each item's processing (CreateMemory plus source
// indexing) to d. A non-positive d disables the deadline, letting a worker
// block indefinitely on a hung call. When the deadline fires the item is
// marked failed with the context error and the worker moves on.
func WithItemTimeout(d time.Duration) Option {
	return func(p *Pool) { p.itemTimeout = d }
}

// WithMetrics wires the pool's Prometheus instruments into reg, mirroring the
// LightRAG client's WithMetrics/Registry convention. A nil reg disables
// registration (the metrics still exist but are never gathered), so call sites
// that omit this option stay unchanged.
func WithMetrics(reg prometheus.Registerer) Option {
	return func(p *Pool) { p.metrics = newMetrics(reg) }
}

// NewPool returns a Pool backed by the given store and runner with size worker goroutines.
func NewPool(store JobStore, runner itemRunner, size int, opts ...Option) *Pool {
	return newPool(store, runner, size, 256, nil, opts...)
}

// NewPoolWithSources is NewPool plus a sink that indexes (repo, file_path,
// track_id) after each successful CreateMemory. sources may be nil.
func NewPoolWithSources(store JobStore, runner itemRunner, size int, sources SourceSink, opts ...Option) *Pool {
	return newPool(store, runner, size, 256, sources, opts...)
}

func newPool(store JobStore, runner itemRunner, size, buf int, sources SourceSink, opts ...Option) *Pool {
	if size < 1 {
		size = 1
	}
	if buf < 1 {
		buf = 1
	}
	p := &Pool{
		store:   store,
		runner:  runner,
		size:    size,
		sources: sources,
		metrics: newMetrics(nil),
		notify:  make(chan string, buf),
		stop:    make(chan struct{}),
	}
	for _, o := range opts {
		o(p)
	}
	return p
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

// Notify queues the given job ID for processing. When the notify channel is
// full the job ID is dropped and counted in ingest_notify_dropped_total; Resume
// re-queues any such jobs at startup, so the drop is recoverable but must stay
// observable (the silent loss class that caused the 0.2.2 stuck-queue incident).
func (p *Pool) Notify(jobID string) {
	select {
	case p.notify <- jobID:
	default:
		p.metrics.incNotifyDropped()
	}
}

// Resume re-queues every unfinished (queued or running) job found at startup
// and returns how many it scheduled. This recovers jobs that were enqueued but
// never notified (or whose notify was dropped) before a restart.
//
// It first resets any items left 'running' by a mid-item crash back to
// 'pending', so the orphans are reclaimed rather than skipped (ClaimNextItem
// only claims 'pending'). This runs before notifying, and the resumed jobs are
// not yet in the notify channel, so no worker is claiming them concurrently.
func (p *Pool) Resume(ctx context.Context) (int, error) {
	if _, err := p.store.ResetRunningItems(ctx); err != nil {
		return 0, err
	}
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
		p.metrics.incInFlight()
		start := time.Now()
		runErr := p.runItem(ctx, item)
		dur := time.Since(start).Seconds()
		_ = p.store.MarkItemDone(ctx, jobID, item.IdempotencyKey, runErr)
		p.metrics.decInFlight()
		p.metrics.observeItem(dur, itemResult(runErr))

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
		p.metrics.incJob(jobSucceeded)
	case final.Done == 0:
		final.Status = memory.JobStatusFailed
		p.metrics.incJob(jobFailed)
	default:
		final.Status = memory.JobStatusPartial
		p.metrics.incJob(jobPartial)
	}
	final.UpdatedAt = time.Now()
	_ = p.store.UpdateJob(ctx, final)
}

// itemResult classifies a per-item run error for ingest_items_total: a fired
// per-item deadline (context.DeadlineExceeded) is reported as timeout, any
// other error as error, and a nil error as success.
func itemResult(err error) string {
	switch {
	case err == nil:
		return resultSuccess
	case errors.Is(err, context.DeadlineExceeded):
		return resultTimeout
	default:
		return resultError
	}
}

// runItem processes a single item, applying the per-item timeout when one is
// configured. MarkItemDone/IncrementJobProgress use the parent ctx so progress
// is recorded even when the item's own deadline fired.
func (p *Pool) runItem(ctx context.Context, it memory.IngestItem) error {
	if p.itemTimeout <= 0 {
		return p.processItem(ctx, it)
	}
	ctx, cancel := context.WithTimeout(ctx, p.itemTimeout)
	defer cancel()
	return p.processItem(ctx, it)
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
