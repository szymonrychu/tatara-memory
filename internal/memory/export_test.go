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

// NewReaperWithFakeStore constructs a Reaper backed by a FakeTombstoneStore for
// unit tests that do not need a real Postgres database.
func NewReaperWithFakeStore(store *FakeTombstoneStore, lr trackStatuser, logger *slog.Logger, reg prometheus.Registerer) *Reaper {
	return newReaper(store, lr, logger, reg)
}
