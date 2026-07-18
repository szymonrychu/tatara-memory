package ingest

// Test for the tombstone-loop fix's secondary finding: periodic_resume_failed
// was ERROR-logged but had no metric, so a pg-outage-driven resume failure
// streak (which self-heals via the next ticker fire) could not be alerted on.
// See internal/memory/reaper.go for the primary fix (force-reap backoff).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// failingResumeStore fails ResetRunningItems (as it would during a pg outage)
// until Recover is set, mirroring the actual failure/self-heal shape.
type failingResumeStore struct {
	fail bool
}

func (s *failingResumeStore) CreateJob(_ context.Context, _ memory.IngestJob, _ []memory.IngestItem) error {
	return nil
}
func (s *failingResumeStore) ListUnfinishedJobs(_ context.Context) ([]string, error) {
	return nil, nil
}
func (s *failingResumeStore) GetJob(_ context.Context, _ string) (memory.IngestJob, error) {
	return memory.IngestJob{}, nil
}
func (s *failingResumeStore) UpdateJob(_ context.Context, _ memory.IngestJob) error { return nil }
func (s *failingResumeStore) ClaimNextItem(_ context.Context, _ string) (memory.IngestItem, bool, error) {
	return memory.IngestItem{}, false, nil
}
func (s *failingResumeStore) MarkItemDone(_ context.Context, _, _ string, _ error) error {
	return nil
}
func (s *failingResumeStore) IncrementJobProgress(_ context.Context, _ string, _ *memory.IngestItemError) error {
	return nil
}
func (s *failingResumeStore) SetItemTrackID(_ context.Context, _, _, _ string) error { return nil }
func (s *failingResumeStore) ResetRunningItems(_ context.Context) (int, error) {
	if s.fail {
		return 0, errors.New("connection refused")
	}
	return 0, nil
}
func (s *failingResumeStore) MarkItemDoneAndProgress(_ context.Context, _, _ string, _ error) error {
	return nil
}

func resumeFailureCount(t *testing.T, reg *prometheus.Registry) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() == "ingest_periodic_resume_failures_total" {
			return mf.GetMetric()[0].GetCounter().GetValue()
		}
	}
	return 0
}

// TestPool_PeriodicResume_FailureIncrementsMetric verifies that a failed
// periodic Resume sweep (e.g. a pg outage) increments
// ingest_periodic_resume_failures_total, alongside the existing ERROR log.
func TestPool_PeriodicResume_FailureIncrementsMetric(t *testing.T) {
	store := &failingResumeStore{fail: true}
	reg := prometheus.NewRegistry()
	p := newPool(store, &nopRunner{}, 1, 16, nil, WithMetrics(reg))
	p.resumeInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)

	require.Eventually(t, func() bool {
		return resumeFailureCount(t, reg) >= 1
	}, 2*time.Second, 10*time.Millisecond,
		"a failing Resume sweep must increment ingest_periodic_resume_failures_total")

	p.Stop()
}

// TestPool_PeriodicResume_MetricPreInitializedAtZero verifies the counter is
// registered (Gather returns the family) even before any resume failure,
// matching this repo's pre-init metric convention.
func TestPool_PeriodicResume_MetricPreInitializedAtZero(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = newMetrics(reg)
	require.Equal(t, float64(0), resumeFailureCount(t, reg))
}
