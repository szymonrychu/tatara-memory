package ingest

import (
	"context"
	"errors"
	"log/slog"
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

// defaultResumeInterval is the period between periodic Resume sweeps.
// A dropped/queued job is picked up within this window without requiring a restart.
const defaultResumeInterval = 5 * time.Minute

// Pool is an async worker pool that processes queued ingest jobs.
type Pool struct {
	store          JobStore
	runner         itemRunner
	size           int
	sources        SourceSink
	itemTimeout    time.Duration
	resumeInterval time.Duration
	metrics        *metrics
	log            *slog.Logger
	notify         chan string
	stop           chan struct{}
	wg             sync.WaitGroup
	mu             sync.Mutex
	started        bool
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

// WithLogger injects a structured logger into the pool for job start/finish and
// item-failure log lines. Defaults to slog.Default() when not set.
func WithLogger(l *slog.Logger) Option {
	return func(p *Pool) { p.log = l }
}

func newPool(store JobStore, runner itemRunner, size, buf int, sources SourceSink, opts ...Option) *Pool {
	if size < 1 {
		size = 1
	}
	if buf < 1 {
		buf = 1
	}
	p := &Pool{
		store:          store,
		runner:         runner,
		size:           size,
		sources:        sources,
		resumeInterval: defaultResumeInterval,
		metrics:        newMetrics(nil),
		log:            slog.Default(),
		notify:         make(chan string, buf),
		stop:           make(chan struct{}),
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Start launches the worker goroutines and the periodic resume sweeper.
// Calling Start more than once is a no-op.
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
	// Periodic sweeper: re-queues any dropped/stuck jobs so starvation resolves
	// without a manual restart (finding 7). Resume is idempotent and cheap.
	p.wg.Add(1)
	go p.periodicResume(ctx)
}

func (p *Pool) periodicResume(ctx context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(p.resumeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := p.Resume(ctx)
			if err != nil {
				p.log.ErrorContext(ctx, "ingest.pool.periodic_resume_failed", "err", err)
			} else if n > 0 {
				p.log.InfoContext(ctx, "ingest.pool.periodic_resume",
					"action", "periodic_resume",
					"requeued", n,
				)
			}
		}
	}
}

// Stop signals all workers to exit and waits for them to finish.
func (p *Pool) Stop() {
	close(p.stop)
	p.wg.Wait()
}

// Notify queues the given job ID for processing. When the notify channel is
// full the job ID is dropped and counted in ingest_notify_dropped_total.
// Dropped jobs are NOT recovered in-process: they remain 'queued' until the
// next process start, when Resume re-queues all unfinished jobs. The metric is
// the only signal that a drop occurred; alert on it so starvation is visible.
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
// It first calls ResetRunningItems to move items left 'running' by a mid-item
// crash back to 'pending', so the orphans are reclaimed rather than skipped
// (ClaimNextItem only claims 'pending'). ResetRunningItems completes as a
// single atomic statement before any job ID is sent to the notify channel, so
// orphan reclaim is correct even though workers may already be running when
// Resume is called (Start is typically called first at the app layer).
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
	jobStart := time.Now()
	j, err := p.store.GetJob(ctx, jobID)
	if err != nil {
		p.log.ErrorContext(ctx, "ingest.job.get_failed",
			"job_id", jobID,
			"err", err,
		)
		return
	}
	// Guard against duplicate/stale notifies: a job that is already terminal
	// (Succeeded, Failed, Partial) must not be re-finalized. Returning here
	// prevents double-counting ingest_jobs_total and a spurious UpdateJob call.
	if j.Status.Terminal() {
		return
	}
	p.log.InfoContext(ctx, "ingest.job.start",
		"action", "ingest_job_start",
		"job_id", jobID,
	)
	if j.Status == memory.JobStatusQueued {
		j.Status = memory.JobStatusRunning
		j.UpdatedAt = time.Now()
		if err := p.store.UpdateJob(ctx, j); err != nil {
			p.log.ErrorContext(ctx, "ingest.job.update_failed",
				"job_id", jobID,
				"err", err,
			)
			p.metrics.incStoreOpError()
		}
	}
	for {
		// Respect shutdown between items so the context cancel is not the only
		// exit path inside the drain loop.
		select {
		case <-p.stop:
			return
		case <-ctx.Done():
			return
		default:
		}

		item, ok, err := p.store.ClaimNextItem(ctx, jobID)
		if err != nil || !ok {
			break
		}
		p.metrics.incInFlight()
		start := time.Now()
		runErr := p.runItem(ctx, jobID, item)
		dur := time.Since(start).Seconds()
		if err := p.store.MarkItemDone(ctx, jobID, item.IdempotencyKey, runErr); err != nil {
			p.log.ErrorContext(ctx, "ingest.item.mark_done_failed",
				"job_id", jobID,
				"idempotency_key", item.IdempotencyKey,
				"err", err,
			)
			p.metrics.incStoreOpError()
		}
		p.metrics.decInFlight()
		p.metrics.observeItem(dur, itemResult(runErr))

		var itemErr *memory.IngestItemError
		if runErr != nil {
			itemErr = &memory.IngestItemError{
				IdempotencyKey: item.IdempotencyKey,
				Error:          runErr.Error(),
			}
			p.log.WarnContext(ctx, "ingest.item.error",
				"job_id", jobID,
				"idempotency_key", item.IdempotencyKey,
				"err", runErr,
			)
		}
		if err := p.store.IncrementJobProgress(ctx, jobID, itemErr); err != nil {
			p.log.ErrorContext(ctx, "ingest.item.progress_failed",
				"job_id", jobID,
				"idempotency_key", item.IdempotencyKey,
				"err", err,
			)
			p.metrics.incStoreOpError()
		}
	}
	final, err := p.store.GetJob(ctx, jobID)
	if err != nil {
		p.log.ErrorContext(ctx, "ingest.job.finalize_get_failed",
			"job_id", jobID,
			"err", err,
		)
		return
	}
	// Only finalize when every item is accounted for. If items are still
	// in-flight (e.g. two workers racing on the same job via duplicate notifies),
	// Done+Failed < Total; leave the job running so the other worker completes it.
	if final.Done+final.Failed < final.Total {
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
	if err := p.store.UpdateJob(ctx, final); err != nil {
		p.log.ErrorContext(ctx, "ingest.job.finalize_update_failed",
			"job_id", jobID,
			"status", string(final.Status),
			"err", err,
		)
		p.metrics.incStoreOpError()
	}
	p.log.InfoContext(ctx, "ingest.job.done",
		"action", "ingest_job_done",
		"job_id", jobID,
		"status", string(final.Status),
		"done", final.Done,
		"failed", final.Failed,
		"total", final.Total,
		"duration_ms", time.Since(jobStart).Milliseconds(),
	)
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
func (p *Pool) runItem(ctx context.Context, jobID string, it memory.IngestItem) error {
	if p.itemTimeout <= 0 {
		return p.processItem(ctx, jobID, it)
	}
	ctx, cancel := context.WithTimeout(ctx, p.itemTimeout)
	defer cancel()
	return p.processItem(ctx, jobID, it)
}

func (p *Pool) processItem(ctx context.Context, jobID string, it memory.IngestItem) error {
	// If TrackID is already set, CreateMemory succeeded on a prior attempt.
	// Skip re-insertion to avoid duplicate LightRAG documents on retry.
	if it.TrackID == "" {
		created, err := p.runner.CreateMemory(ctx, memory.Memory{
			ID:       it.IdempotencyKey,
			Text:     it.Text,
			Metadata: it.Metadata,
		})
		if err != nil {
			return err
		}
		it.TrackID = created.ID
		// Persist the track_id so a retry of this item (e.g. crash between here
		// and MarkItemDone) sees TrackID != "" and skips re-insertion.
		if err := p.store.SetItemTrackID(ctx, jobID, it.IdempotencyKey, it.TrackID); err != nil {
			// Non-fatal: the idempotency guard is best-effort. Log and continue.
			p.log.WarnContext(ctx, "ingest.item.set_track_id_failed",
				"job_id", jobID,
				"idempotency_key", it.IdempotencyKey,
				"err", err,
			)
		}
	}
	if p.sources != nil {
		repo := it.Metadata["repo"]
		file := it.Metadata["file_path"]
		if repo != "" && file != "" && it.TrackID != "" {
			if err := p.sources.Add(ctx, repo, file, it.TrackID); err != nil {
				// Source indexing is a secondary projection. Its failure does not
				// invalidate the primary CreateMemory, so the item is still
				// counted as done. Log at WARN and count for observability.
				p.log.WarnContext(ctx, "ingest.item.source_index_failed",
					"job_id", jobID,
					"idempotency_key", it.IdempotencyKey,
					"repo", repo,
					"file_path", file,
					"err", err,
				)
				p.metrics.incSourceIndexError()
			}
		}
	}
	return nil
}
