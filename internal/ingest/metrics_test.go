package ingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// scriptedRunner drives the three item outcomes the pool metrics must
// distinguish: a plain success, a generic error, and a hung call that only the
// per-item timeout unsticks (surfacing as context.DeadlineExceeded).
type scriptedRunner struct{}

func (scriptedRunner) CreateMemory(ctx context.Context, m memory.Memory) (memory.Memory, error) {
	switch m.Text {
	case "err":
		return memory.Memory{}, errors.New("boom")
	case "hang":
		<-ctx.Done()
		return memory.Memory{}, ctx.Err()
	default:
		return m, nil
	}
}

func counterValue(t *testing.T, reg *prometheus.Registry, name, label, value string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.Metric {
			if label == "" && len(m.GetLabel()) == 0 {
				return m.GetCounter().GetValue()
			}
			for _, lp := range m.GetLabel() {
				if lp.GetName() == label && lp.GetValue() == value {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	t.Fatalf("counter %s{%s=%q} not found", name, label, value)
	return 0
}

func gaugeValue(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf.Metric[0].GetGauge().GetValue()
		}
	}
	t.Fatalf("gauge %s not found", name)
	return 0
}

func histogramCount(t *testing.T, reg *prometheus.Registry, name string) uint64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf.Metric[0].GetHistogram().GetSampleCount()
		}
	}
	t.Fatalf("histogram %s not found", name)
	return 0
}

// WithMetrics must register every family so a fresh pool gathers them at zero,
// matching the LightRAG newMetrics precedent (no series appears only after the
// first event).
func TestPoolMetrics_RegisteredAtZero(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = newPool(NewMemStore(), scriptedRunner{}, 1, 1, nil, WithMetrics(reg))

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"ingest_items_total",
		"ingest_item_duration_seconds",
		"ingest_jobs_total",
		"ingest_items_in_flight",
		"ingest_notify_dropped_total",
	} {
		if !names[want] {
			t.Errorf("metric family %s not registered at construction", want)
		}
	}

	// Pre-initialized result labels return at zero before any item runs.
	for _, result := range []string{"success", "error", "timeout"} {
		if got := counterValue(t, reg, "ingest_items_total", "result", result); got != 0 {
			t.Errorf("ingest_items_total{result=%q} = %v, want 0", result, got)
		}
	}
}

// Driving one job through a success, a generic error, and a hung item under a
// short per-item timeout must move every instrument, with timeout distinguished
// from error and the in-flight gauge returning to zero.
func TestPoolMetrics_Increment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := prometheus.NewRegistry()
	store := NewMemStore()
	pool := newPool(store, scriptedRunner{}, 1, 256, nil,
		WithItemTimeout(50*time.Millisecond), WithMetrics(reg))
	pool.Start(ctx)
	defer pool.Stop()

	e := NewEnqueuer(store, nil)
	job, err := e.Enqueue(ctx, []memory.IngestItem{
		{Text: "ok"}, {Text: "err"}, {Text: "hang"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pool.Notify(job.ID)

	deadline := time.Now().Add(3 * time.Second)
	for {
		j, _ := store.GetJob(ctx, job.ID)
		if j.Status.Terminal() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not reach a terminal status")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := counterValue(t, reg, "ingest_items_total", "result", "success"); got != 1 {
		t.Errorf("items success = %v, want 1", got)
	}
	if got := counterValue(t, reg, "ingest_items_total", "result", "error"); got != 1 {
		t.Errorf("items error = %v, want 1", got)
	}
	if got := counterValue(t, reg, "ingest_items_total", "result", "timeout"); got != 1 {
		t.Errorf("items timeout = %v, want 1", got)
	}
	if got := histogramCount(t, reg, "ingest_item_duration_seconds"); got != 3 {
		t.Errorf("duration sample count = %v, want 3", got)
	}
	// One success, two failures (error + timeout) finalizes the job partial.
	if got := counterValue(t, reg, "ingest_jobs_total", "status", "partial"); got != 1 {
		t.Errorf("jobs partial = %v, want 1", got)
	}
	if got := gaugeValue(t, reg, "ingest_items_in_flight"); got != 0 {
		t.Errorf("in_flight = %v, want 0 after drain", got)
	}
}

// A full notify channel must count the dropped job ID rather than lose it
// silently (the 0.2.2 stuck-queue failure class). A buffer of 1 with no worker
// draining makes the overflow deterministic.
func TestPoolMetrics_NotifyDropCounted(t *testing.T) {
	reg := prometheus.NewRegistry()
	pool := newPool(NewMemStore(), scriptedRunner{}, 1, 1, nil, WithMetrics(reg))

	pool.Notify("first")  // fills the buffer (cap 1)
	pool.Notify("second") // channel full -> dropped and counted

	if got := counterValue(t, reg, "ingest_notify_dropped_total", "", ""); got != 1 {
		t.Errorf("ingest_notify_dropped_total = %v, want 1", got)
	}
}

// A nil registry (the default when WithMetrics is omitted) must be a no-op:
// the pool still runs and Notify still drops safely without panicking on a nil
// metrics struct.
func TestPoolMetrics_NilRegistryNoOp(t *testing.T) {
	pool := newPool(NewMemStore(), scriptedRunner{}, 1, 1, nil)
	pool.Notify("first")
	pool.Notify("second") // would panic if metrics were nil
}
