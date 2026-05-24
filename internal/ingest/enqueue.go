package ingest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// ErrDuplicateKey is returned when a batch contains two items with the same idempotency key.
var ErrDuplicateKey = errors.New("ingest: duplicate idempotency key in batch")

// Enqueuer creates queued ingest jobs in a JobStore.
type Enqueuer struct {
	store JobStore
	now   func() time.Time
}

// NewEnqueuer returns an Enqueuer backed by the given JobStore.
func NewEnqueuer(s JobStore) *Enqueuer {
	return &Enqueuer{store: s, now: time.Now}
}

func newJobID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "job_" + hex.EncodeToString(b[:])
}

func newItemID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "itm_" + hex.EncodeToString(b[:])
}

// Enqueue validates items, assigns missing idempotency keys, and stores a queued job.
func (e *Enqueuer) Enqueue(ctx context.Context, items []memory.IngestItem) (memory.IngestJob, error) {
	if len(items) == 0 {
		return memory.IngestJob{}, errors.New("ingest: empty items")
	}
	for i := range items {
		if items[i].IdempotencyKey == "" {
			items[i].IdempotencyKey = newItemID()
		}
	}
	seen := make(map[string]struct{}, len(items))
	for _, it := range items {
		if _, ok := seen[it.IdempotencyKey]; ok {
			return memory.IngestJob{}, ErrDuplicateKey
		}
		seen[it.IdempotencyKey] = struct{}{}
	}
	now := e.now()
	job := memory.IngestJob{
		ID:        newJobID(),
		Status:    memory.JobStatusQueued,
		Total:     len(items),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := e.store.CreateJob(ctx, job, items); err != nil {
		return memory.IngestJob{}, err
	}
	return job, nil
}

// GetJob delegates to the underlying JobStore.
func (e *Enqueuer) GetJob(ctx context.Context, id string) (memory.IngestJob, error) {
	return e.store.GetJob(ctx, id)
}
