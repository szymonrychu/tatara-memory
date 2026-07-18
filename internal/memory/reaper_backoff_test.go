package memory_test

// Unit tests for the reaper's force-reap backoff (fix/tombstone-reaper-backoff).
// Root cause: forceTick used to re-check every aged tombstone against lightrag
// on every 5-minute tick forever, with no attempt counter or backoff, hammering
// lightrag and the WARN log for tombstones stuck upstream indefinitely. These
// tests cover the fix: attempts increment and the next check is deferred via an
// exponential backoff (capped at 24h), backed-off tombstones are skipped from
// the candidate set until due, and a confirmed-gone doc still deletes the row
// regardless of any prior backoff state.

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestReaper_ForcedPath_StillPresent_IncrementsAttemptsAndDefersNextCheck(t *testing.T) {
	cases := []struct {
		name          string
		startAttempts int
		wantDelay     time.Duration
	}{
		{"first attempt", 0, time.Hour},
		{"second attempt", 1, 2 * time.Hour},
		{"third attempt", 2, 4 * time.Hour},
		{"ramps toward cap", 4, 16 * time.Hour},
		{"capped at 24h", 10, 24 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lr := &fakeReaperLRFull{presentFor: map[string]bool{"stuck": true}}
			store := memory.NewFakeTombstoneStoreWithAged(nil, []string{"stuck"})
			store.SetForceState("stuck", tc.startAttempts, time.Time{}) // eligible now
			reg := prometheus.NewRegistry()
			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			r := memory.NewReaperWithFakeStore(store, lr, logger, reg)

			before := time.Now()
			memory.ForceTickForTest(r, context.Background())

			require.Equal(t, tc.startAttempts+1, store.AttemptsFor("stuck"),
				"a still-present doc must increment force_reap_attempts")
			next := store.NextCheckAtFor("stuck")
			require.WithinDuration(t, before.Add(tc.wantDelay), next, 2*time.Second,
				"next_force_check_at must reflect the backoff schedule for the new attempt count")
			require.True(t, store.HasAged("stuck"),
				"a still-present doc's tombstone must not be deleted")
		})
	}
}

func TestReaper_ForcedPath_BackedOffTombstone_NotRecheckedYet(t *testing.T) {
	lr := &fakeReaperLRCountCalls{}
	store := memory.NewFakeTombstoneStoreWithAged(nil, []string{"stuck"})
	store.SetForceState("stuck", 3, time.Now().Add(2*time.Hour)) // not due yet
	reg := prometheus.NewRegistry()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := memory.NewReaperWithFakeStore(store, lr, logger, reg)

	memory.ForceTickForTest(r, context.Background())

	require.Equal(t, 0, lr.calls,
		"a backed-off tombstone must not be re-checked against lightrag before its next_force_check_at")
	require.Equal(t, 3, store.AttemptsFor("stuck"), "attempts must not change when the id is skipped")
}

func TestReaper_ForcedPath_ConfirmedGone_DeletesDespitePriorBackoff(t *testing.T) {
	// Default fakeReaperLRFull behavior (not in presentFor/emptyFor) is 404 -> gone.
	lr := &fakeReaperLRFull{}
	store := memory.NewFakeTombstoneStoreWithAged(nil, []string{"stuck"})
	store.SetForceState("stuck", 5, time.Time{}) // eligible now, after several prior still-present checks
	reg := prometheus.NewRegistry()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := memory.NewReaperWithFakeStore(store, lr, logger, reg)

	memory.ForceTickForTest(r, context.Background())

	require.False(t, store.HasAged("stuck"),
		"a confirmed-gone doc must delete the tombstone regardless of accumulated backoff state")

	mfs, err := reg.Gather()
	require.NoError(t, err)
	var forced float64
	for _, mf := range mfs {
		if mf.GetName() != "tatara_memory_tombstone_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "op" && lp.GetValue() == "forced" {
					forced = m.GetCounter().GetValue()
				}
			}
		}
	}
	require.InDelta(t, 1.0, forced, 0.0001, "forced reap counter must be incremented")
}
