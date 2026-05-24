package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

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
