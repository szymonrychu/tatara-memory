package ingest

// Tests for obs-scaffold round-3 finding 7 in internal/ingest.
// Finding 7: dropped/stuck jobs must be recovered periodically without a process
// restart. Start() must launch a periodicResume goroutine that calls Resume on
// each tick so a dropped Notify re-queues within defaultResumeInterval.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// periodicResumeStore counts ListUnfinishedJobs calls to detect periodic resume ticks.
type periodicResumeStore struct {
	listCalls atomic.Int32
}

func (s *periodicResumeStore) CreateJob(_ context.Context, _ memory.IngestJob, _ []memory.IngestItem) error {
	return nil
}
func (s *periodicResumeStore) ListUnfinishedJobs(_ context.Context) ([]string, error) {
	s.listCalls.Add(1)
	return nil, nil
}
func (s *periodicResumeStore) GetJob(_ context.Context, _ string) (memory.IngestJob, error) {
	return memory.IngestJob{}, nil
}
func (s *periodicResumeStore) UpdateJob(_ context.Context, _ memory.IngestJob) error { return nil }
func (s *periodicResumeStore) ClaimNextItem(_ context.Context, _ string) (memory.IngestItem, bool, error) {
	return memory.IngestItem{}, false, nil
}
func (s *periodicResumeStore) MarkItemDone(_ context.Context, _, _ string, _ error) error {
	return nil
}
func (s *periodicResumeStore) IncrementJobProgress(_ context.Context, _ string, _ *memory.IngestItemError) error {
	return nil
}
func (s *periodicResumeStore) SetItemTrackID(_ context.Context, _, _, _ string) error { return nil }
func (s *periodicResumeStore) ResetRunningItems(_ context.Context) (int, error)       { return 0, nil }
func (s *periodicResumeStore) MarkItemDoneAndProgress(_ context.Context, _, _ string, _ error) error {
	return nil
}

// nopRunner satisfies itemRunner with no-ops.
type nopRunner struct{}

func (n *nopRunner) CreateMemory(_ context.Context, _ memory.Memory) (memory.Memory, error) {
	return memory.Memory{}, nil
}

// TestPool_PeriodicResume_CallsResumePeriodically verifies that Start launches a
// goroutine that calls Resume (via ListUnfinishedJobs) on a periodic tick.
func TestPool_PeriodicResume_CallsResumePeriodically(t *testing.T) {
	store := &periodicResumeStore{}
	p := newPool(store, &nopRunner{}, 1, 16, nil)
	// Override resume interval to something very short for the test.
	p.resumeInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)

	// Wait for at least 2 periodic resume sweeps.
	require.Eventually(t, func() bool {
		return store.listCalls.Load() >= 2
	}, 2*time.Second, 10*time.Millisecond,
		"periodicResume must call ListUnfinishedJobs at least twice within 2s (finding 7)")

	p.Stop()
}
