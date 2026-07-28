package memory_test

// TDD for tatara-memory#90 / #91, service half:
//
//  1. lightrag.BusyError (the pipeline lock never freed inside the retry
//     budget) is transient. Without an explicit mapping wrapUpstream's
//     fallthrough turns it into ErrUpstream -> HTTP 502, a permanent error
//     the ingest client does not retry.
//  2. The purge is serial across reconcile files and each LightRAG delete
//     holds the pipeline lock for 6-42s, so the next file's delete meets a
//     lock held by our own previous delete. Waiting per call therefore has to
//     be bounded by ONE budget for the whole batch, or a wide reconcile would
//     run past any client timeout. Exhausting it must be transient and must
//     leave the work done so far durable, so the client's retry resumes.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// busyErrLR fails every DeleteDocs the way the HTTP client now does once the
// pipeline lock has stayed held for the whole busy-retry budget.
type busyErrLR struct {
	*fake.Client
}

func (b *busyErrLR) DeleteDocs(_ context.Context, _ lightrag.DeleteDocRequest) (*lightrag.DeleteDocByIdResponse, error) {
	return nil, &lightrag.BusyError{Op: lightrag.OpDeleteDocs, Attempts: 12, Waited: 45 * time.Second}
}

// lockHoldingLR models the real cost of a delete: LightRAG accepts it, then
// holds the pipeline lock while it rebuilds the graph, so the next delete in
// the batch waits inside the client for hold before it gets through.
type lockHoldingLR struct {
	*fake.Client
	hold  time.Duration
	calls int
}

func (l *lockHoldingLR) DeleteDocs(ctx context.Context, req lightrag.DeleteDocRequest) (*lightrag.DeleteDocByIdResponse, error) {
	l.calls++
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(l.hold):
	}
	return l.Client.DeleteDocs(ctx, req)
}

func TestDeleteMemory_BusyErrorIsTransient(t *testing.T) {
	ctx := context.Background()
	inner := fake.New()
	svc := memory.NewService(&busyErrLR{Client: inner}, nil)

	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "hello"})
	require.NoError(t, err)

	err = svc.DeleteMemory(ctx, m.ID)
	require.Error(t, err)
	require.ErrorIs(t, err, memory.ErrTransient,
		"an exhausted pipeline-lock wait is transient (503 + Retry-After), the client must retry it")
	require.NotErrorIs(t, err, memory.ErrUpstream,
		"a held pipeline lock must not be reported as a permanent upstream failure (502)")
}

func TestDeleteMemoriesBySources_PurgeBudgetBoundsTheWholeBatch(t *testing.T) {
	ctx := context.Background()
	inner := fake.New()
	lr := &lockHoldingLR{Client: inner, hold: 150 * time.Millisecond}
	src := newInMemSources()
	svc := memory.NewServiceWithSources(lr, newInMemTombstone(), src).
		WithPurgeBudget(400 * time.Millisecond)

	files := []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go", "g.go", "h.go"}
	for _, f := range files {
		m, err := svc.CreateMemory(ctx, memory.Memory{Text: "text " + f})
		require.NoError(t, err)
		require.NoError(t, src.Add(ctx, "repoX", f, m.ID))
	}

	start := time.Now()
	purged, err := svc.DeleteMemoriesBySources(ctx, "repoX", files)
	elapsed := time.Since(start)

	require.Error(t, err, "8 files x 150ms of lock wait cannot fit a 400ms purge budget")
	require.ErrorIs(t, err, memory.ErrTransient,
		"running out of purge budget is backpressure, not a permanent failure")
	require.Less(t, elapsed, 2*time.Second,
		"the budget must bound the whole batch, not each file")
	require.Greater(t, purged, 0, "files purged before the budget ran out must be reported")
	require.Less(t, lr.calls, len(files), "the batch must stop, not attempt every file")

	// Progress is durable: the files already purged are cleared from the source
	// index, so the client's retry resumes on the remainder instead of redoing
	// the whole reconcile.
	remaining := 0
	for _, f := range files {
		ids, err := src.TrackIDs(ctx, "repoX", f)
		require.NoError(t, err)
		remaining += len(ids)
	}
	require.Less(t, remaining, len(files), "purged files must not be re-purged on retry")
}
