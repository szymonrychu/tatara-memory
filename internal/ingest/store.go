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
	MarkItemDone(ctx context.Context, jobID, idemKey string, runErr error) error
	ListUnfinishedJobs(ctx context.Context) ([]string, error)
	RequeueOrphanedItems(ctx context.Context) (int, error)
}
