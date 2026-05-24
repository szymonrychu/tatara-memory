package memory_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// errClient is a minimal lightrag.Client stub that returns a fixed error for every method.
type errClient struct{ err error }

func (e *errClient) InsertDocument(_ context.Context, _ lightrag.InsertRequest) (*lightrag.InsertResponse, error) {
	return nil, e.err
}
func (e *errClient) GetDocument(_ context.Context, _ string) (*lightrag.Document, error) {
	return nil, e.err
}
func (e *errClient) DeleteDocument(_ context.Context, _ string) error { return e.err }
func (e *errClient) Query(_ context.Context, _ lightrag.QueryRequest) (*lightrag.QueryResponse, error) {
	return nil, e.err
}
func (e *errClient) QueryDescribe(_ context.Context, _ lightrag.QueryRequest) (*lightrag.DescribeResponse, error) {
	return nil, e.err
}
func (e *errClient) ListEntities(_ context.Context, _ string) ([]lightrag.Entity, error) {
	return nil, e.err
}
func (e *errClient) GetEntity(_ context.Context, _ string) (*lightrag.Entity, error) {
	return nil, e.err
}
func (e *errClient) UpdateEntity(_ context.Context, _ string, _ lightrag.EntityUpdate) (*lightrag.Entity, error) {
	return nil, e.err
}
func (e *errClient) ListEdges(_ context.Context) ([]lightrag.Edge, error) { return nil, e.err }
func (e *errClient) CreateEdge(_ context.Context, _ lightrag.Edge) (*lightrag.Edge, error) {
	return nil, e.err
}
func (e *errClient) DeleteEdge(_ context.Context, _ string) error { return e.err }
func (e *errClient) Health(_ context.Context) error               { return e.err }

func TestServiceCreateGetDelete(t *testing.T) {
	ctx := context.Background()
	svc := memory.NewService(fake.New())

	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "hello"})
	require.NoError(t, err)
	require.NotEmpty(t, m.ID)

	got, err := svc.GetMemory(ctx, m.ID)
	require.NoError(t, err)
	require.Equal(t, "hello", got.Text)

	require.NoError(t, svc.DeleteMemory(ctx, m.ID))

	_, err = svc.GetMemory(ctx, m.ID)
	require.ErrorIs(t, err, memory.ErrNotFound)
}

func TestServiceQuery(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SeedMatches([]lightrag.Match{{ID: "m1", Score: 0.9, Text: "alpha bravo"}})
	svc := memory.NewService(f)

	_, err := svc.CreateMemory(ctx, memory.Memory{Text: "alpha bravo"})
	require.NoError(t, err)

	res, err := svc.Query(ctx, memory.Query{Mode: memory.QueryModeHybrid, Text: "alpha"})
	require.NoError(t, err)
	require.NotEmpty(t, res.Matches)

	_, err = svc.Query(ctx, memory.Query{Mode: memory.QueryMode("nope"), Text: "x"})
	require.Error(t, err)
}

func TestServiceDescribe(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SeedDescribe("tatara is a smelting furnace", nil)
	svc := memory.NewService(f)
	_, err := svc.CreateMemory(ctx, memory.Memory{Text: "tatara is a smelting furnace"})
	require.NoError(t, err)

	r, err := svc.Describe(ctx, memory.Query{Mode: memory.QueryModeHybrid, Text: "what is tatara"})
	require.NoError(t, err)
	require.NotEmpty(t, r.Response)
}

func TestServiceEntities(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SeedEntity(lightrag.Entity{ID: "e1", Name: "tatara", Type: "concept"})
	svc := memory.NewService(f)

	e, err := svc.GetEntity(ctx, "e1")
	require.NoError(t, err)
	require.Equal(t, "tatara", e.Name)

	got, err := svc.SearchEntities(ctx, "tatara")
	require.NoError(t, err)
	require.Len(t, got, 1)

	updated, err := svc.PatchEntity(ctx, "e1", memory.Entity{Description: "smelter"})
	require.NoError(t, err)
	require.Equal(t, "smelter", updated.Description)
}

func TestServiceEdges(t *testing.T) {
	ctx := context.Background()
	svc := memory.NewService(fake.New())

	edge, err := svc.CreateEdge(ctx, memory.Edge{From: "a", To: "b", Relation: "rel"})
	require.NoError(t, err)
	require.NotEmpty(t, edge.ID)

	list, err := svc.ListEdges(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, svc.DeleteEdge(ctx, edge.ID))
}

func TestServiceNotFoundWrapped(t *testing.T) {
	ctx := context.Background()
	svc := memory.NewService(fake.New())
	_, err := svc.GetMemory(ctx, "nonexistent")
	require.True(t, errors.Is(err, memory.ErrNotFound), "expected ErrNotFound, got: %v", err)
}

func TestServiceErrTransient(t *testing.T) {
	ctx := context.Background()

	t.Run("http 500 yields ErrTransient", func(t *testing.T) {
		svc := memory.NewService(&errClient{err: &lightrag.HTTPError{Status: http.StatusInternalServerError, Path: "/documents/x"}})
		_, err := svc.GetMemory(ctx, "x")
		require.ErrorIs(t, err, memory.ErrTransient, "expected ErrTransient, got: %v", err)
	})

	t.Run("http 503 yields ErrTransient", func(t *testing.T) {
		svc := memory.NewService(&errClient{err: &lightrag.HTTPError{Status: http.StatusServiceUnavailable, Path: "/documents/x"}})
		_, err := svc.GetMemory(ctx, "x")
		require.ErrorIs(t, err, memory.ErrTransient, "expected ErrTransient, got: %v", err)
	})

	t.Run("context.DeadlineExceeded yields ErrTransient", func(t *testing.T) {
		svc := memory.NewService(&errClient{err: context.DeadlineExceeded})
		_, err := svc.GetMemory(ctx, "x")
		require.ErrorIs(t, err, memory.ErrTransient, "expected ErrTransient, got: %v", err)
	})

	t.Run("context.Canceled yields ErrTransient", func(t *testing.T) {
		svc := memory.NewService(&errClient{err: context.Canceled})
		_, err := svc.GetMemory(ctx, "x")
		require.ErrorIs(t, err, memory.ErrTransient, "expected ErrTransient, got: %v", err)
	})

	t.Run("http 404 still yields ErrNotFound", func(t *testing.T) {
		svc := memory.NewService(&errClient{err: &lightrag.HTTPError{Status: http.StatusNotFound, Path: "/documents/x"}})
		_, err := svc.GetMemory(ctx, "x")
		require.ErrorIs(t, err, memory.ErrNotFound, "expected ErrNotFound, got: %v", err)
	})

	t.Run("http 400 yields ErrUpstream", func(t *testing.T) {
		svc := memory.NewService(&errClient{err: &lightrag.HTTPError{Status: http.StatusBadRequest, Path: "/documents/x"}})
		_, err := svc.GetMemory(ctx, "x")
		require.ErrorIs(t, err, memory.ErrUpstream, "expected ErrUpstream, got: %v", err)
	})
}
