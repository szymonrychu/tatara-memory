package memory_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// inMemSources is a thread-safe in-memory sources index for unit tests.
type inMemSources struct {
	mu  sync.Mutex
	idx map[string][]string // key repo|file -> track_ids
}

func newInMemSources() *inMemSources { return &inMemSources{idx: map[string][]string{}} }

func key(repo, file string) string { return repo + "|" + file }

func (s *inMemSources) Add(_ context.Context, repo, file, trackID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(repo, file)
	for _, id := range s.idx[k] {
		if id == trackID {
			return nil
		}
	}
	s.idx[k] = append(s.idx[k], trackID)
	return nil
}

func (s *inMemSources) TrackIDs(_ context.Context, repo, file string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]string(nil), s.idx[key(repo, file)]...)
	return out, nil
}

func (s *inMemSources) DeleteByFile(_ context.Context, repo, file string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(repo, file)
	n := int64(len(s.idx[k]))
	delete(s.idx, k)
	return n, nil
}

func TestDeleteMemoriesBySource(t *testing.T) {
	ctx := context.Background()
	lr := fake.New()
	tomb := newInMemTombstone()
	src := newInMemSources()
	svc := memory.NewServiceWithSources(lr, tomb, src)

	// Create two memories and index them under repoX/a.go.
	m1, err := svc.CreateMemory(ctx, memory.Memory{Text: "one"})
	require.NoError(t, err)
	m2, err := svc.CreateMemory(ctx, memory.Memory{Text: "two"})
	require.NoError(t, err)
	require.NoError(t, src.Add(ctx, "repoX", "a.go", m1.ID))
	require.NoError(t, src.Add(ctx, "repoX", "a.go", m2.ID))

	n, err := svc.DeleteMemoriesBySource(ctx, "repoX", "a.go")
	require.NoError(t, err)
	require.Equal(t, 2, n)

	// Both track_ids are tombstoned (DeleteMemory was called for each).
	d1, _ := tomb.IsDeleted(ctx, m1.ID)
	d2, _ := tomb.IsDeleted(ctx, m2.ID)
	require.True(t, d1)
	require.True(t, d2)

	// Index rows are gone.
	ids, err := src.TrackIDs(ctx, "repoX", "a.go")
	require.NoError(t, err)
	require.Empty(t, ids)

	// Idempotent: a second call purges nothing.
	n, err = svc.DeleteMemoriesBySource(ctx, "repoX", "a.go")
	require.NoError(t, err)
	require.Equal(t, 0, n)
}
