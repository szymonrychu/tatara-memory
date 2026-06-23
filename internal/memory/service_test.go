package memory_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// inMemTombstone is a thread-safe in-memory tombstone for unit tests.
type inMemTombstone struct {
	mu      sync.Mutex
	deleted map[string]struct{}
}

func newInMemTombstone() *inMemTombstone {
	return &inMemTombstone{deleted: map[string]struct{}{}}
}

func (t *inMemTombstone) Mark(_ context.Context, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.deleted[id] = struct{}{}
	return nil
}

func (t *inMemTombstone) Unmark(_ context.Context, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.deleted, id)
	return nil
}

func (t *inMemTombstone) IsDeleted(_ context.Context, id string) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.deleted[id]
	return ok, nil
}

// errClient is a minimal lightrag.Client stub that returns a fixed error for every method.
type errClient struct{ err error }

func (e *errClient) InsertText(_ context.Context, _ lightrag.InsertTextRequest) (*lightrag.InsertResponse, error) {
	return nil, e.err
}
func (e *errClient) TrackStatus(_ context.Context, _ string) (*lightrag.TrackStatusResponse, error) {
	return nil, e.err
}
func (e *errClient) DeleteDocs(_ context.Context, _ lightrag.DeleteDocRequest) (*lightrag.DeleteDocByIdResponse, error) {
	return nil, e.err
}
func (e *errClient) Query(_ context.Context, _ lightrag.QueryRequest) (*lightrag.QueryResponse, error) {
	return nil, e.err
}
func (e *errClient) QueryData(_ context.Context, _ lightrag.QueryRequest) (*lightrag.QueryDataResponse, error) {
	return nil, e.err
}
func (e *errClient) EntityExists(_ context.Context, _ string) (bool, error) { return false, e.err }
func (e *errClient) CreateEntity(_ context.Context, _ lightrag.EntityCreateRequest) (*lightrag.EntityResponse, error) {
	return nil, e.err
}
func (e *errClient) UpdateEntity(_ context.Context, _ lightrag.EntityUpdateRequest) (*lightrag.EntityResponse, error) {
	return nil, e.err
}
func (e *errClient) DeleteEntity(_ context.Context, _ lightrag.DeleteEntityRequest) error {
	return e.err
}
func (e *errClient) LabelSearch(_ context.Context, _ string) ([]string, error) { return nil, e.err }
func (e *errClient) Graph(_ context.Context, _ string, _, _ int) (*lightrag.KnowledgeGraph, error) {
	return nil, e.err
}
func (e *errClient) CreateRelation(_ context.Context, _ lightrag.RelationCreateRequest) (*lightrag.RelationResponse, error) {
	return nil, e.err
}
func (e *errClient) DeleteRelation(_ context.Context, _ lightrag.DeleteRelationRequest) error {
	return e.err
}
func (e *errClient) Health(_ context.Context) error { return e.err }

func TestServiceCreateGetDelete(t *testing.T) {
	ctx := context.Background()
	svc := memory.NewService(fake.New(), nil)

	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "hello"})
	require.NoError(t, err)
	require.NotEmpty(t, m.ID, "track_id should populate Memory.ID")

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
	f.SeedQueryResponse(lightrag.QueryResponse{
		Response: "ignored",
		References: []lightrag.ReferenceItem{
			{ReferenceID: "r1", FilePath: "/a.md", Content: []string{"alpha bravo"}},
		},
	})
	svc := memory.NewService(f, nil)

	res, err := svc.Query(ctx, memory.Query{Mode: memory.QueryModeHybrid, Text: "alpha"})
	require.NoError(t, err)
	require.Len(t, res.Matches, 1)
	require.Equal(t, "r1", res.Matches[0].ID)

	// include_references must be set or LightRAG omits the reference list and
	// Matches comes back empty (see issue tatara-cli#21 symptom 2).
	sent := f.LastQuery()
	require.NotNil(t, sent.IncludeReferences)
	require.True(t, *sent.IncludeReferences)

	_, err = svc.Query(ctx, memory.Query{Mode: memory.QueryMode("nope"), Text: "x"})
	require.Error(t, err)
}

func TestServiceQueryData(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SeedQueryDataResponse(lightrag.QueryDataResponse{
		Status: "success",
		Data: map[string]any{"chunks": []any{
			map[string]any{"content": "alpha bravo", "chunk_id": "c1", "reference_id": "r1"},
			map[string]any{"content": "charlie", "chunk_id": "c2", "reference_id": "r2"},
		}},
	})
	svc := memory.NewService(f, nil)

	res, err := svc.QueryData(ctx, memory.Query{Mode: memory.QueryModeHybrid, Text: "alpha", TopK: 5})
	require.NoError(t, err)
	require.Len(t, res.Matches, 2)
	require.Equal(t, "c1", res.Matches[0].ID)
	require.Equal(t, "alpha bravo", res.Matches[0].Text)
	require.Greater(t, res.Matches[0].Score, res.Matches[1].Score, "matches are score-ranked by retrieval order")

	// include_chunk_content must be set or LightRAG omits chunk text; top_k is
	// mirrored into chunk_top_k so the depth bounds the ranked chunk window.
	sent := f.LastQuery()
	require.NotNil(t, sent.IncludeChunks)
	require.True(t, *sent.IncludeChunks)
	require.Equal(t, 5, sent.TopK)
	require.Equal(t, 5, sent.ChunkTopK)

	_, err = svc.QueryData(ctx, memory.Query{Mode: memory.QueryMode("nope"), Text: "x"})
	require.Error(t, err)
}

func TestServiceQueryData_FiltersTombstonedChunks(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	tomb := newInMemTombstone()
	svc := memory.NewService(f, tomb)

	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "secret"})
	require.NoError(t, err)
	f.SeedQueryDataResponse(lightrag.QueryDataResponse{
		Status: "success",
		Data: map[string]any{"chunks": []any{
			map[string]any{"content": "secret", "chunk_id": "c1", "reference_id": m.ID},
			map[string]any{"content": "public", "chunk_id": "c2", "reference_id": "other-id"},
		}},
	})

	require.NoError(t, svc.DeleteMemory(ctx, m.ID))

	res, err := svc.QueryData(ctx, memory.Query{Mode: memory.QueryModeHybrid, Text: "q"})
	require.NoError(t, err)
	require.Len(t, res.Matches, 1, "tombstoned chunk is dropped")
	require.Equal(t, "c2", res.Matches[0].ID)
}

func TestServiceDescribe(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SeedQueryResponse(lightrag.QueryResponse{
		Response: "tatara is a smelting furnace",
		References: []lightrag.ReferenceItem{
			{ReferenceID: "r1", FilePath: "/wiki/tatara.md"},
		},
	})
	svc := memory.NewService(f, nil)

	r, err := svc.Describe(ctx, memory.Query{Mode: memory.QueryModeHybrid, Text: "what is tatara"})
	require.NoError(t, err)
	require.Equal(t, "tatara is a smelting furnace", r.Response)
	require.Equal(t, []string{"/wiki/tatara.md"}, r.Sources)
}

func TestServiceEntities(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SeedEntity("tatara", map[string]any{"entity_type": "concept", "description": "furnace"})
	svc := memory.NewService(f, nil)

	e, err := svc.GetEntity(ctx, "tatara")
	require.NoError(t, err)
	require.Equal(t, "tatara", e.Name)
	require.Equal(t, "concept", e.Type)

	got, err := svc.SearchEntities(ctx, "tatara")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "tatara", got[0].Name)

	updated, err := svc.PatchEntity(ctx, "tatara", memory.Entity{Description: "smelter"})
	require.NoError(t, err)
	require.Equal(t, "smelter", updated.Description)
}

func TestServiceEdges(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SeedEntity("a", nil)
	f.SeedEntity("b", nil)
	svc := memory.NewService(f, nil)

	edge, err := svc.CreateEdge(ctx, memory.Edge{From: "a", To: "b", Relation: "rel"})
	require.NoError(t, err)
	require.Equal(t, memory.EncodeEdgeID("a", "b"), edge.ID)

	list, err := svc.ListEdges(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, svc.DeleteEdge(ctx, edge.ID))
}

func TestServiceDeleteEdgeRejectsMalformedID(t *testing.T) {
	ctx := context.Background()
	svc := memory.NewService(fake.New(), nil)
	err := svc.DeleteEdge(ctx, "no-separator")
	require.ErrorIs(t, err, memory.ErrInvalid)
}

func TestServiceNotFoundWrapped(t *testing.T) {
	ctx := context.Background()
	svc := memory.NewService(fake.New(), nil)
	_, err := svc.GetMemory(ctx, "nonexistent")
	require.True(t, errors.Is(err, memory.ErrNotFound), "expected ErrNotFound, got: %v", err)
}

func TestServiceErrTransient(t *testing.T) {
	ctx := context.Background()

	t.Run("http 500 yields ErrTransient", func(t *testing.T) {
		svc := memory.NewService(&errClient{err: &lightrag.HTTPError{Status: http.StatusInternalServerError}}, nil)
		_, err := svc.GetMemory(ctx, "x")
		require.ErrorIs(t, err, memory.ErrTransient)
	})

	t.Run("http 503 yields ErrTransient", func(t *testing.T) {
		svc := memory.NewService(&errClient{err: &lightrag.HTTPError{Status: http.StatusServiceUnavailable}}, nil)
		_, err := svc.GetMemory(ctx, "x")
		require.ErrorIs(t, err, memory.ErrTransient)
	})

	t.Run("context.DeadlineExceeded yields ErrTransient", func(t *testing.T) {
		svc := memory.NewService(&errClient{err: context.DeadlineExceeded}, nil)
		_, err := svc.GetMemory(ctx, "x")
		require.ErrorIs(t, err, memory.ErrTransient)
	})

	t.Run("context.Canceled yields ErrTransient", func(t *testing.T) {
		svc := memory.NewService(&errClient{err: context.Canceled}, nil)
		_, err := svc.GetMemory(ctx, "x")
		require.ErrorIs(t, err, memory.ErrTransient)
	})

	t.Run("http 404 yields ErrNotFound", func(t *testing.T) {
		svc := memory.NewService(&errClient{err: &lightrag.HTTPError{Status: http.StatusNotFound}}, nil)
		_, err := svc.GetMemory(ctx, "x")
		require.ErrorIs(t, err, memory.ErrNotFound)
	})

	t.Run("http 400 yields ErrUpstream", func(t *testing.T) {
		svc := memory.NewService(&errClient{err: &lightrag.HTTPError{Status: http.StatusBadRequest}}, nil)
		_, err := svc.GetMemory(ctx, "x")
		require.ErrorIs(t, err, memory.ErrUpstream)
	})
}

func newTestServiceWithTombstone(t *testing.T) (*memory.Service, *fake.Client, *inMemTombstone) {
	t.Helper()
	f := fake.New()
	tomb := newInMemTombstone()
	svc := memory.NewService(f, tomb)
	return svc, f, tomb
}

func TestService_DeleteThenGet_ReturnsNotFound(t *testing.T) {
	ctx := context.Background()

	s, _, tomb := newTestServiceWithTombstone(t)

	// Seed a doc via CreateMemory (fake wires InsertText -> TrackStatus)
	m, err := s.CreateMemory(ctx, memory.Memory{Text: "hello"})
	require.NoError(t, err)

	// GET works before delete
	got, err := s.GetMemory(ctx, m.ID)
	require.NoError(t, err)
	require.Equal(t, m.ID, got.ID)

	// DELETE
	require.NoError(t, s.DeleteMemory(ctx, m.ID))

	// Tombstone is set
	set, err := tomb.IsDeleted(ctx, m.ID)
	require.NoError(t, err)
	require.True(t, set)

	// GET now returns ErrNotFound regardless of lightrag state
	_, err = s.GetMemory(ctx, m.ID)
	require.ErrorIs(t, err, memory.ErrNotFound)
}
