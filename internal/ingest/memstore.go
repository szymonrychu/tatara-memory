package ingest

import (
	"context"
	"sync"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type memItem struct {
	memory.IngestItem
	status string
	err    string
}

type memJobBundle struct {
	job   memory.IngestJob
	items []*memItem
}

// MemStore is a thread-safe in-memory implementation of JobStore for tests.
type MemStore struct {
	mu   sync.Mutex
	jobs map[string]*memJobBundle
}

// NewMemStore returns a ready-to-use in-memory JobStore.
func NewMemStore() *MemStore {
	return &MemStore{jobs: make(map[string]*memJobBundle)}
}

// CreateJob stores the job and its items. Returns ErrJobExists if the job ID is taken.
func (s *MemStore) CreateJob(_ context.Context, j memory.IngestJob, items []memory.IngestItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[j.ID]; ok {
		return ErrJobExists
	}
	b := &memJobBundle{job: j}
	for _, it := range items {
		b.items = append(b.items, &memItem{IngestItem: it, status: "pending"})
	}
	s.jobs[j.ID] = b
	return nil
}

// GetJob retrieves the current snapshot of a job by ID.
func (s *MemStore) GetJob(_ context.Context, id string) (memory.IngestJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.jobs[id]
	if !ok {
		return memory.IngestJob{}, ErrJobNotFound
	}
	return b.job, nil
}

// UpdateJob replaces the stored job state.
func (s *MemStore) UpdateJob(_ context.Context, j memory.IngestJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.jobs[j.ID]
	if !ok {
		return ErrJobNotFound
	}
	j.UpdatedAt = time.Now()
	b.job = j
	return nil
}

// ClaimNextItem atomically marks the next pending item as running and returns it.
func (s *MemStore) ClaimNextItem(_ context.Context, jobID string) (memory.IngestItem, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.jobs[jobID]
	if !ok {
		return memory.IngestItem{}, false, ErrJobNotFound
	}
	for _, it := range b.items {
		if it.status == "pending" {
			it.status = "running"
			return it.IngestItem, true, nil
		}
	}
	return memory.IngestItem{}, false, nil
}

// MarkItemDone records the outcome of a processed item.
func (s *MemStore) MarkItemDone(_ context.Context, jobID, key string, runErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.jobs[jobID]
	if !ok {
		return ErrJobNotFound
	}
	for _, it := range b.items {
		if it.IdempotencyKey == key {
			if runErr != nil {
				it.status = "failed"
				it.err = runErr.Error()
			} else {
				it.status = "done"
			}
			return nil
		}
	}
	return nil
}

// ListRunningJobs returns the IDs of all jobs in the running state.
func (s *MemStore) ListRunningJobs(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id, b := range s.jobs {
		if b.job.Status == memory.JobStatusRunning {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
