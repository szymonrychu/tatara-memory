package memory_test

// Unit tests for the Reaper that do not require a real Postgres database.
// Integration tests with a live DB are in reaper_test.go (build tag: integration).

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// fakeReaperLRUnit simulates TrackStatus responses for unit tests.
type fakeReaperLRUnit struct {
	errFor map[string]error
}

func (f *fakeReaperLRUnit) TrackStatus(_ context.Context, id string) (*lightrag.TrackStatusResponse, error) {
	if err, ok := f.errFor[id]; ok {
		return nil, err
	}
	return &lightrag.TrackStatusResponse{}, nil
}

func reapCounterFor(t *testing.T, reg *prometheus.Registry, op string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != "tatara_memory_tombstone_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "op" && lp.GetValue() == op {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

// TestReaper_CheckError_CountedAndLogged verifies that a non-404 upstream error
// on TrackStatus increments tatara_memory_tombstone_total{op="check_error"} and
// does NOT reap the tombstone (finding 8).
func TestReaper_CheckError_CountedAndLogged(t *testing.T) {
	transientErr := &lightrag.HTTPError{Status: http.StatusInternalServerError, Body: "oops"}
	notFoundErr := &lightrag.HTTPError{Status: http.StatusNotFound}
	lr := &fakeReaperLRUnit{
		errFor: map[string]error{
			"track-error": transientErr,
			"track-404":   notFoundErr,
		},
	}

	fakeStore := &memory.FakeTombstoneStore{IDs: []string{"track-error", "track-404"}}
	reg := prometheus.NewRegistry()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := memory.NewReaperWithFakeStore(fakeStore, lr, logger, reg)

	memory.TickForTest(r, context.Background())

	// check_error must be incremented for the 500 response
	checkErrs := reapCounterFor(t, reg, "check_error")
	require.InDelta(t, 1.0, checkErrs, 0.0001, "check_error counter not incremented for non-404 upstream error")

	// The 500-id tombstone must NOT have been reaped
	require.Contains(t, fakeStore.IDs, "track-error", "tombstone with non-404 error must not be reaped")

	// The 404 must have been reaped
	require.NotContains(t, fakeStore.IDs, "track-404", "tombstone with 404 must be reaped")

	reaped := reapCounterFor(t, reg, "reaped")
	require.InDelta(t, 1.0, reaped, 0.0001, "reaped counter must be incremented for 404")
}

// TestReaper_CheckError_MetricRegisteredAtZero verifies check_error is
// pre-initialized so Prometheus returns the family from Gather even before any
// error occurs (finding 8).
func TestReaper_CheckError_MetricRegisteredAtZero(t *testing.T) {
	reg := prometheus.NewRegistry()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	fakeStore := &memory.FakeTombstoneStore{}
	_ = memory.NewReaperWithFakeStore(fakeStore, &fakeReaperLRUnit{}, logger, reg)

	checkErrs := reapCounterFor(t, reg, "check_error")
	require.InDelta(t, 0.0, checkErrs, 0.0001, "check_error counter must be pre-registered at zero")
}
