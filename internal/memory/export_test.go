package memory

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// TickForTest exposes the unexported tick method to package memory_test.
func TickForTest(r *Reaper, ctx context.Context) {
	r.tick(ctx)
}

// MigrationNames returns the ordered list of migration names tracked by Migrate.
// Used by unit tests to verify versioning without a real database.
func MigrationNames() []string {
	names := make([]string, len(migrations))
	for i, m := range migrations {
		names[i] = m.name
	}
	return names
}

// CreateSchemaMigrationsSQL returns the DDL used to bootstrap the tracker table.
func CreateSchemaMigrationsSQL() string {
	return createSchemaMigrations
}

// FakeTombstoneStore is an in-memory reapStore for unit tests.
type FakeTombstoneStore struct {
	IDs []string
}

func (f *FakeTombstoneStore) List(_ context.Context, _ int) ([]string, error) {
	out := make([]string, len(f.IDs))
	copy(out, f.IDs)
	return out, nil
}

func (f *FakeTombstoneStore) Delete(_ context.Context, id string) error {
	out := f.IDs[:0]
	for _, x := range f.IDs {
		if x != id {
			out = append(out, x)
		}
	}
	f.IDs = out
	return nil
}

func (f *FakeTombstoneStore) ListOlderThan(_ context.Context, _ time.Duration, _ int) ([]ForceCandidate, error) {
	return nil, nil
}

func (f *FakeTombstoneStore) RecordForceCheckStillPresent(_ context.Context, _ string, _ time.Time) error {
	return nil
}

// NewReaperWithFakeStore constructs a Reaper backed by a FakeTombstoneStore for
// unit tests that do not need a real Postgres database.
func NewReaperWithFakeStore(store reapStore, lr trackStatuser, logger *slog.Logger, reg prometheus.Registerer) *Reaper {
	return newReaper(store, lr, logger, reg)
}

// ForceTickForTest runs only the forced-reap path (ListOlderThan per-id verify loop) of the reaper.
func ForceTickForTest(r *Reaper, ctx context.Context) {
	r.forceTick(ctx)
}

// SetReaperInterval sets the reaper tick interval (used to make per-tick timeout tests fast).
func SetReaperInterval(r *Reaper, d time.Duration) {
	r.interval = d
}

// forceState tracks the per-id force-reap backoff state a real TombstoneStore
// would persist in the force_reap_attempts/next_force_check_at columns.
type forceState struct {
	attempts    int
	nextCheckAt time.Time // zero = eligible immediately
}

// FakeTombstoneStoreWithAged is a reapStore for testing the forced-reap path.
// Aged IDs are returned by ListOlderThan (subject to backoff eligibility) and
// can be deleted by Delete.
type FakeTombstoneStoreWithAged struct {
	Live  []string
	Aged  []string
	state map[string]*forceState
}

// NewFakeTombstoneStoreWithAged constructs the store with separate live and aged id sets.
func NewFakeTombstoneStoreWithAged(live, aged []string) *FakeTombstoneStoreWithAged {
	f := &FakeTombstoneStoreWithAged{
		Live:  append([]string(nil), live...),
		Aged:  append([]string(nil), aged...),
		state: make(map[string]*forceState, len(aged)),
	}
	for _, id := range aged {
		f.state[id] = &forceState{}
	}
	return f
}

// SetForceState seeds attempts/nextCheckAt for id, for tests that need to
// start mid-backoff-schedule without waiting for real time to elapse.
func (f *FakeTombstoneStoreWithAged) SetForceState(id string, attempts int, nextCheckAt time.Time) {
	f.state[id] = &forceState{attempts: attempts, nextCheckAt: nextCheckAt}
}

// AttemptsFor returns id's current force_reap_attempts count (0 if unset).
func (f *FakeTombstoneStoreWithAged) AttemptsFor(id string) int {
	if st, ok := f.state[id]; ok {
		return st.attempts
	}
	return 0
}

// NextCheckAtFor returns id's current next_force_check_at (zero if unset).
func (f *FakeTombstoneStoreWithAged) NextCheckAtFor(id string) time.Time {
	if st, ok := f.state[id]; ok {
		return st.nextCheckAt
	}
	return time.Time{}
}

func (f *FakeTombstoneStoreWithAged) List(_ context.Context, limit int) ([]string, error) {
	out := make([]string, 0, limit)
	for i, id := range f.Live {
		if i >= limit {
			break
		}
		out = append(out, id)
	}
	return out, nil
}

func (f *FakeTombstoneStoreWithAged) Delete(_ context.Context, id string) error {
	out := f.Live[:0]
	for _, x := range f.Live {
		if x != id {
			out = append(out, x)
		}
	}
	f.Live = out
	out2 := f.Aged[:0]
	for _, x := range f.Aged {
		if x != id {
			out2 = append(out2, x)
		}
	}
	f.Aged = out2
	delete(f.state, id)
	return nil
}

// ListOlderThan returns up to limit Aged IDs whose backoff state makes them
// eligible for re-verification now (ignores maxAge; the Aged set is
// pre-populated by the test), paired with their current attempt count.
func (f *FakeTombstoneStoreWithAged) ListOlderThan(_ context.Context, _ time.Duration, limit int) ([]ForceCandidate, error) {
	var out []ForceCandidate
	now := time.Now()
	for _, id := range f.Aged {
		if st, ok := f.state[id]; ok && !st.nextCheckAt.IsZero() && st.nextCheckAt.After(now) {
			continue // backed off, not yet due
		}
		attempts := 0
		if st, ok := f.state[id]; ok {
			attempts = st.attempts
		}
		out = append(out, ForceCandidate{TrackID: id, Attempts: attempts})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// RecordForceCheckStillPresent bumps id's attempt count and sets its
// next_force_check_at, mirroring TombstoneStore.RecordForceCheckStillPresent.
func (f *FakeTombstoneStoreWithAged) RecordForceCheckStillPresent(_ context.Context, id string, nextCheckAt time.Time) error {
	st, ok := f.state[id]
	if !ok {
		st = &forceState{}
		f.state[id] = st
	}
	st.attempts++
	st.nextCheckAt = nextCheckAt
	return nil
}

// HasAged reports whether id is still in the Aged set.
func (f *FakeTombstoneStoreWithAged) HasAged(id string) bool {
	for _, x := range f.Aged {
		if x == id {
			return true
		}
	}
	return false
}
