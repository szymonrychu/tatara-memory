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

func (f *FakeTombstoneStore) ReapOlderThan(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

func (f *FakeTombstoneStore) ListOlderThan(_ context.Context, _ time.Duration) ([]string, error) {
	return nil, nil
}

// NewReaperWithFakeStore constructs a Reaper backed by a FakeTombstoneStore for
// unit tests that do not need a real Postgres database.
func NewReaperWithFakeStore(store reapStore, lr trackStatuser, logger *slog.Logger, reg prometheus.Registerer) *Reaper {
	return newReaper(store, lr, logger, reg)
}

// ForceTickForTest runs only the forced-reap path (ReapOlderThan per-id loop) of the reaper.
func ForceTickForTest(r *Reaper, ctx context.Context) {
	r.forceTick(ctx)
}

// FakeTombstoneStoreWithAged is a reapStore for testing the forced-reap path.
// Aged IDs are returned by ListAged and can be deleted by Delete.
type FakeTombstoneStoreWithAged struct {
	Live []string
	Aged []string
}

// NewFakeTombstoneStoreWithAged constructs the store with separate live and aged id sets.
func NewFakeTombstoneStoreWithAged(live, aged []string) *FakeTombstoneStoreWithAged {
	return &FakeTombstoneStoreWithAged{
		Live: append([]string(nil), live...),
		Aged: append([]string(nil), aged...),
	}
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
	return nil
}

func (f *FakeTombstoneStoreWithAged) ReapOlderThan(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

// ListOlderThan returns the Aged IDs (ignores maxAge; the set is pre-populated by the test).
func (f *FakeTombstoneStoreWithAged) ListOlderThan(_ context.Context, _ time.Duration) ([]string, error) {
	return append([]string(nil), f.Aged...), nil
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
