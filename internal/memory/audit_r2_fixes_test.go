package memory_test

// TDD tests for audit-r2 findings in internal/memory.
// Each test is written to fail before the corresponding fix is applied.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// fakeReaperLRFull lets per-id control of TrackStatus: present (200+docs), empty
// (200+no docs), 404, or arbitrary error.
type fakeReaperLRFull struct {
	presentFor map[string]bool
	emptyFor   map[string]bool
	errFor     map[string]error
}

func (f *fakeReaperLRFull) TrackStatus(_ context.Context, id string) (*lightrag.TrackStatusResponse, error) {
	if err, ok := f.errFor[id]; ok {
		return nil, err
	}
	if f.presentFor[id] {
		return &lightrag.TrackStatusResponse{
			TrackID:   id,
			Documents: []lightrag.DocStatusResponse{{ID: "doc-1"}},
		}, nil
	}
	if f.emptyFor[id] {
		return &lightrag.TrackStatusResponse{TrackID: id}, nil
	}
	return nil, &lightrag.HTTPError{Status: http.StatusNotFound}
}

// Finding 2: Fast-path reap must treat 200+empty-documents as "confirmed gone".
func TestReaper_FastPath_EmptyDocuments_IsReaped(t *testing.T) {
	lr := &fakeReaperLRFull{
		emptyFor: map[string]bool{"track-empty": true},
		errFor:   map[string]error{"track-404": &lightrag.HTTPError{Status: http.StatusNotFound}},
	}
	fakeStore := &memory.FakeTombstoneStore{IDs: []string{"track-empty", "track-404"}}
	reg := prometheus.NewRegistry()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := memory.NewReaperWithFakeStore(fakeStore, lr, logger, reg)

	memory.TickForTest(r, context.Background())

	// 200+empty must be treated the same as 404: reap the tombstone.
	require.NotContains(t, fakeStore.IDs, "track-empty",
		"tombstone with 200+empty docs must be reaped (finding 2)")
	require.NotContains(t, fakeStore.IDs, "track-404",
		"tombstone with 404 must also be reaped")
}

// Finding 1: Force-reap path must skip tombstones whose doc is still present,
// emit force_skipped_still_present metric, and only delete confirmed-gone ones.
func TestReaper_ForcedPath_SkipsStillPresentDocs(t *testing.T) {
	lr := &fakeReaperLRFull{
		presentFor: map[string]bool{"track-present": true},
		// track-gone -> 404 (default)
	}
	fakeStore := memory.NewFakeTombstoneStoreWithAged(
		nil,                                     // live (fast-path)
		[]string{"track-present", "track-gone"}, // aged (force-path)
	)
	reg := prometheus.NewRegistry()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := memory.NewReaperWithFakeStore(fakeStore, lr, logger, reg)

	memory.ForceTickForTest(r, context.Background())

	// "track-present" must NOT be deleted.
	require.True(t, fakeStore.HasAged("track-present"),
		"force-reap must not delete a tombstone whose doc is still present (finding 1)")

	// "track-gone" must be deleted.
	require.False(t, fakeStore.HasAged("track-gone"),
		"force-reap must delete a tombstone whose doc is confirmed gone (finding 1)")

	// force_skipped_still_present counter must be 1.
	mfs, err := reg.Gather()
	require.NoError(t, err)
	var skipped float64
	for _, mf := range mfs {
		if mf.GetName() != "tatara_memory_tombstone_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "op" && lp.GetValue() == "force_skipped_still_present" {
					skipped = m.GetCounter().GetValue()
				}
			}
		}
	}
	require.InDelta(t, 1.0, skipped, 0.001,
		"force_skipped_still_present metric must be incremented (finding 1)")
}

// Finding 3: CreateMemory must return ErrUpstream when InsertResponse.TrackID is empty.
func TestCreateMemory_EmptyTrackID(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SetInsertResponse("success", "") // status OK but empty track_id
	svc := memory.NewService(f, nil)

	_, err := svc.CreateMemory(ctx, memory.Memory{Text: "test"})
	require.Error(t, err, "CreateMemory must error when TrackID is empty (finding 3)")
	require.ErrorIs(t, err, memory.ErrUpstream,
		"empty TrackID must map to ErrUpstream (finding 3)")
}

// Finding 3b: CreateMemory must return ErrUpstream when InsertResponse.Status
// indicates a logical failure.
func TestCreateMemory_LogicalFailureStatus(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SetInsertResponse("failure", "") // non-success status, empty track_id
	svc := memory.NewService(f, nil)

	_, err := svc.CreateMemory(ctx, memory.Memory{Text: "test"})
	require.Error(t, err, "CreateMemory must error on logical failure response (finding 3)")
	require.ErrorIs(t, err, memory.ErrUpstream,
		"logical failure must map to ErrUpstream (finding 3)")
}

// Finding 7: DeleteMemoriesBySource must return the already-purged count on
// partial failure, not 0.
func TestDeleteMemoriesBySource_PartialFailureReturnsCount(t *testing.T) {
	ctx := context.Background()
	innerLR := fake.New()
	tomb := newInMemTombstone()
	src := newInMemSources()

	// Create two memories and index them.
	m1, err := memory.NewService(innerLR, nil).CreateMemory(ctx, memory.Memory{Text: "a"})
	require.NoError(t, err)
	m2, err := memory.NewService(innerLR, nil).CreateMemory(ctx, memory.Memory{Text: "b"})
	require.NoError(t, err)
	require.NoError(t, src.Add(ctx, "repo", "f.go", m1.ID))
	require.NoError(t, src.Add(ctx, "repo", "f.go", m2.ID))

	// Wrap with a fake that returns a 500 on TrackStatus for m2 so DeleteMemory
	// fails for it with ErrTransient (non-NotFound => loop breaks).
	lrErr := &trackStatusErrorer{inner: innerLR, failID: m2.ID,
		err: &lightrag.HTTPError{Status: http.StatusInternalServerError, Body: "injected"}}
	svcErr := memory.NewServiceWithSources(lrErr, tomb, src)

	n, err := svcErr.DeleteMemoriesBySource(ctx, "repo", "f.go")
	require.Error(t, err, "must return error on mid-loop failure")
	// Must return 1 (m1 succeeded), not 0.
	require.Equal(t, 1, n,
		"partial purge must return count of already-purged tracks, not 0 (finding 7)")
}

// trackStatusErrorer wraps a Client to inject an error on TrackStatus for a specific ID.
type trackStatusErrorer struct {
	inner  lightrag.Client
	failID string
	err    error
}

func (c *trackStatusErrorer) InsertText(ctx context.Context, req lightrag.InsertTextRequest) (*lightrag.InsertResponse, error) {
	return c.inner.InsertText(ctx, req)
}
func (c *trackStatusErrorer) TrackStatus(ctx context.Context, id string) (*lightrag.TrackStatusResponse, error) {
	if id == c.failID {
		return nil, c.err
	}
	return c.inner.TrackStatus(ctx, id)
}
func (c *trackStatusErrorer) DeleteDocs(ctx context.Context, req lightrag.DeleteDocRequest) (*lightrag.DeleteDocByIdResponse, error) {
	return c.inner.DeleteDocs(ctx, req)
}
func (c *trackStatusErrorer) Query(ctx context.Context, req lightrag.QueryRequest) (*lightrag.QueryResponse, error) {
	return c.inner.Query(ctx, req)
}
func (c *trackStatusErrorer) QueryData(ctx context.Context, req lightrag.QueryRequest) (*lightrag.QueryDataResponse, error) {
	return c.inner.QueryData(ctx, req)
}
func (c *trackStatusErrorer) EntityExists(ctx context.Context, name string) (bool, error) {
	return c.inner.EntityExists(ctx, name)
}
func (c *trackStatusErrorer) CreateEntity(ctx context.Context, req lightrag.EntityCreateRequest) (*lightrag.EntityResponse, error) {
	return c.inner.CreateEntity(ctx, req)
}
func (c *trackStatusErrorer) UpdateEntity(ctx context.Context, req lightrag.EntityUpdateRequest) (*lightrag.EntityResponse, error) {
	return c.inner.UpdateEntity(ctx, req)
}
func (c *trackStatusErrorer) DeleteEntity(ctx context.Context, req lightrag.DeleteEntityRequest) error {
	return c.inner.DeleteEntity(ctx, req)
}
func (c *trackStatusErrorer) LabelSearch(ctx context.Context, q string) ([]string, error) {
	return c.inner.LabelSearch(ctx, q)
}
func (c *trackStatusErrorer) Graph(ctx context.Context, label string, depth, maxNodes int) (*lightrag.KnowledgeGraph, error) {
	return c.inner.Graph(ctx, label, depth, maxNodes)
}
func (c *trackStatusErrorer) CreateRelation(ctx context.Context, req lightrag.RelationCreateRequest) (*lightrag.RelationResponse, error) {
	return c.inner.CreateRelation(ctx, req)
}
func (c *trackStatusErrorer) DeleteRelation(ctx context.Context, req lightrag.DeleteRelationRequest) error {
	return c.inner.DeleteRelation(ctx, req)
}
func (c *trackStatusErrorer) Health(ctx context.Context) error { return c.inner.Health(ctx) }

// Finding 8: EntityUpdatePayload must not let Properties clobber reserved keys.
func TestEntityUpdatePayload_PropertiesCannotClobberReservedKeys(t *testing.T) {
	patch := memory.Entity{
		Name:        "new-name",
		Type:        "concept",
		Description: "desc",
		Properties: map[string]string{
			"entity_name":  "injected-name",
			"entity_type":  "injected-type",
			"description":  "injected-desc",
			"custom_field": "value",
		},
	}
	data := memory.EntityUpdatePayload(patch)
	require.Equal(t, "new-name", data["entity_name"],
		"entity_name from typed field must win over Properties (finding 8)")
	require.Equal(t, "concept", data["entity_type"],
		"entity_type from typed field must win over Properties (finding 8)")
	require.Equal(t, "desc", data["description"],
		"description from typed field must win over Properties (finding 8)")
	require.Equal(t, "value", data["custom_field"],
		"non-reserved Properties keys must still be copied (finding 8)")
}
