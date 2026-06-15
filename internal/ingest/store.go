// Package ingest provides async batch ingestion of memory items.
package ingest

import (
	"context"
	"errors"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// ErrJobExists is returned when a job with the same ID already exists.
var ErrJobExists = errors.New("ingest: job already exists")

// ErrJobNotFound is returned when the requested job does not exist.
var ErrJobNotFound = errors.New("ingest: job not found")

// JobStore is the persistence interface for ingest jobs and their items.
type JobStore interface {
	CreateJob(ctx context.Context, job memory.IngestJob, items []memory.IngestItem) error
	GetJob(ctx context.Context, id string) (memory.IngestJob, error)
	UpdateJob(ctx context.Context, job memory.IngestJob) error
	ClaimNextItem(ctx context.Context, jobID string) (memory.IngestItem, bool, error)
	// SetItemTrackID persists the LightRAG track_id that was returned by
	// CreateMemory. It must be called immediately after a successful
	// CreateMemory so that a retry of the same item (after a crash between
	// CreateMemory and MarkItemDone) can detect the already-created document
	// via IngestItem.TrackID and skip re-insertion.
	SetItemTrackID(ctx context.Context, jobID, idemKey, trackID string) error
	MarkItemDone(ctx context.Context, jobID, idemKey string, runErr error) error
	// IncrementJobProgress atomically records one processed item: it bumps done
	// (itemErr == nil) or failed (itemErr != nil), appending the error up to
	// maxErrors entries. The increment is atomic so concurrent workers draining
	// the same job cannot clobber each other's counts.
	IncrementJobProgress(ctx context.Context, jobID string, itemErr *memory.IngestItemError) error
	ListUnfinishedJobs(ctx context.Context) ([]string, error)
	// ResetRunningItems moves items stuck in 'running' (a worker crashed
	// mid-item, before MarkItemDone) back to 'pending' for all unfinished
	// jobs. ClaimNextItem only claims 'pending', so without this an orphaned
	// item is never reprocessed and its job drains to a short count. Called on
	// Resume before workers start claiming. Returns how many items were reset.
	ResetRunningItems(ctx context.Context) (int, error)
}
