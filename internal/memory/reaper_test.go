//go:build integration

package memory_test

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

type fakeReaperLR struct {
	notFound map[string]bool
}

func (f *fakeReaperLR) TrackStatus(_ context.Context, id string) (*lightrag.TrackStatusResponse, error) {
	if f.notFound[id] {
		return nil, &lightrag.HTTPError{Status: http.StatusNotFound}
	}
	return &lightrag.TrackStatusResponse{}, nil
}

func TestReaper_Tick_ReapsConfirmedDeleted(t *testing.T) {
	db := openTestDB(t)
	require.NoError(t, memory.Migrate(context.Background(), db))

	store := memory.NewTombstoneStore(db)
	fake := &fakeReaperLR{notFound: map[string]bool{"gone": true, "still-there": false}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := prometheus.NewRegistry()
	r := memory.NewReaper(store, fake, logger, reg)

	ctx := context.Background()
	require.NoError(t, store.Mark(ctx, "gone"))
	require.NoError(t, store.Mark(ctx, "still-there"))

	memory.TickForTest(r, ctx)

	// 'gone' confirmed 404 by lightrag - tombstone should be removed
	gone, err := store.IsDeleted(ctx, "gone")
	require.NoError(t, err)
	require.False(t, gone)

	// 'still-there' lightrag returned non-404 - tombstone should remain
	still, err := store.IsDeleted(ctx, "still-there")
	require.NoError(t, err)
	require.True(t, still)
}

func TestReaper_Tick_ForcedReap(t *testing.T) {
	db := openTestDB(t)
	require.NoError(t, memory.Migrate(context.Background(), db))

	store := memory.NewTombstoneStore(db)
	// fake that never returns 404 - forced reap must clean up old entries
	fake := &fakeReaperLR{notFound: map[string]bool{}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := prometheus.NewRegistry()
	r := memory.NewReaper(store, fake, logger, reg)

	ctx := context.Background()
	require.NoError(t, store.Mark(ctx, "old-entry"))
	_, err := db.ExecContext(ctx, `UPDATE deleted_memories SET deleted_at = now() - interval '25 hours' WHERE track_id = 'old-entry'`)
	require.NoError(t, err)

	memory.TickForTest(r, ctx)

	// forced TTL reap should have removed the entry
	still, err := store.IsDeleted(ctx, "old-entry")
	require.NoError(t, err)
	require.False(t, still)
}
