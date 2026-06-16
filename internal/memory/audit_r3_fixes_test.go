package memory_test

// TDD tests for audit-r3 findings in internal/memory.
// Each test documents which finding it covers and must FAIL before the fix is applied.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// --- Finding 1: DeleteMemory must fail when DeleteDocs returns non-success status ---

// deletionStatusFakeLR wraps a fake Client and overrides DeleteDocs to return a
// success HTTP 200 but with a non-success status field.
type deletionStatusFakeLR struct {
	inner  *fake.Client
	status string
}

func (d *deletionStatusFakeLR) InsertText(ctx context.Context, req lightrag.InsertTextRequest) (*lightrag.InsertResponse, error) {
	return d.inner.InsertText(ctx, req)
}
func (d *deletionStatusFakeLR) TrackStatus(ctx context.Context, id string) (*lightrag.TrackStatusResponse, error) {
	return d.inner.TrackStatus(ctx, id)
}
func (d *deletionStatusFakeLR) DeleteDocs(_ context.Context, _ lightrag.DeleteDocRequest) (*lightrag.DeleteDocByIdResponse, error) {
	return &lightrag.DeleteDocByIdResponse{Status: d.status, Message: "injected"}, nil
}
func (d *deletionStatusFakeLR) Query(ctx context.Context, req lightrag.QueryRequest) (*lightrag.QueryResponse, error) {
	return d.inner.Query(ctx, req)
}
func (d *deletionStatusFakeLR) QueryData(ctx context.Context, req lightrag.QueryRequest) (*lightrag.QueryDataResponse, error) {
	return d.inner.QueryData(ctx, req)
}
func (d *deletionStatusFakeLR) EntityExists(ctx context.Context, name string) (bool, error) {
	return d.inner.EntityExists(ctx, name)
}
func (d *deletionStatusFakeLR) CreateEntity(ctx context.Context, req lightrag.EntityCreateRequest) (*lightrag.EntityResponse, error) {
	return d.inner.CreateEntity(ctx, req)
}
func (d *deletionStatusFakeLR) UpdateEntity(ctx context.Context, req lightrag.EntityUpdateRequest) (*lightrag.EntityResponse, error) {
	return d.inner.UpdateEntity(ctx, req)
}
func (d *deletionStatusFakeLR) DeleteEntity(ctx context.Context, req lightrag.DeleteEntityRequest) error {
	return d.inner.DeleteEntity(ctx, req)
}
func (d *deletionStatusFakeLR) LabelSearch(ctx context.Context, q string) ([]string, error) {
	return d.inner.LabelSearch(ctx, q)
}
func (d *deletionStatusFakeLR) Graph(ctx context.Context, label string, depth, maxNodes int) (*lightrag.KnowledgeGraph, error) {
	return d.inner.Graph(ctx, label, depth, maxNodes)
}
func (d *deletionStatusFakeLR) CreateRelation(ctx context.Context, req lightrag.RelationCreateRequest) (*lightrag.RelationResponse, error) {
	return d.inner.CreateRelation(ctx, req)
}
func (d *deletionStatusFakeLR) DeleteRelation(ctx context.Context, req lightrag.DeleteRelationRequest) error {
	return d.inner.DeleteRelation(ctx, req)
}
func (d *deletionStatusFakeLR) Health(ctx context.Context) error { return d.inner.Health(ctx) }

func TestDeleteMemory_DeleteDocsNonSuccessStatus(t *testing.T) {
	ctx := context.Background()
	inner := fake.New()
	lr := &deletionStatusFakeLR{inner: inner, status: "failure"}
	svc := memory.NewService(lr, nil)

	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "hello"})
	require.NoError(t, err)

	err = svc.DeleteMemory(ctx, m.ID)
	require.Error(t, err, "DeleteMemory must fail when DeleteDocs responds with status=failure (finding 1)")
	require.ErrorIs(t, err, memory.ErrUpstream,
		"non-success DeleteDocs status must map to ErrUpstream (finding 1)")
}

// --- Finding 2: forceTick ListOlderThan must be batch-capped ---

// fakeReaperLRCountCalls counts TrackStatus calls to verify the batch cap.
type fakeReaperLRCountCalls struct {
	calls int
}

func (f *fakeReaperLRCountCalls) TrackStatus(_ context.Context, _ string) (*lightrag.TrackStatusResponse, error) {
	f.calls++
	return nil, &lightrag.HTTPError{Status: http.StatusNotFound}
}

func TestReaper_ForceTick_BatchCapped(t *testing.T) {
	overflowCount := memory.TombstoneReapBatchSize + 10
	aged := make([]string, overflowCount)
	for i := range aged {
		aged[i] = intToStr(i)
	}

	store := memory.NewFakeTombstoneStoreWithAged(nil, aged)
	lr := &fakeReaperLRCountCalls{}
	reg := prometheus.NewRegistry()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := memory.NewReaperWithFakeStore(store, lr, logger, reg)

	memory.ForceTickForTest(r, context.Background())

	require.LessOrEqual(t, lr.calls, memory.TombstoneReapBatchSize,
		"forceTick must cap TrackStatus calls at TombstoneReapBatchSize (finding 2)")
}

func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

// --- Finding 3: tick must respect a per-tick bounded context ---

// blockingTrackStatuser blocks until ctx is done.
type blockingTrackStatuser struct{}

func (b *blockingTrackStatuser) TrackStatus(ctx context.Context, _ string) (*lightrag.TrackStatusResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestReaper_TickTimeout(t *testing.T) {
	store := &memory.FakeTombstoneStore{IDs: []string{"track-slow"}}
	reg := prometheus.NewRegistry()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := memory.NewReaperWithFakeStore(store, &blockingTrackStatuser{}, logger, reg)
	memory.SetReaperInterval(r, 50*time.Millisecond)

	tickCtx := context.Background()
	done := make(chan struct{})
	go func() {
		defer close(done)
		memory.TickForTest(r, tickCtx)
	}()
	select {
	case <-done:
		// tick returned within deadline - good
	case <-time.After(2 * time.Second):
		t.Fatal("tick did not return within 2s; per-tick timeout not applied (finding 3)")
	}
}

// --- Finding 5: Query and Describe must filter tombstoned memories ---

func TestQuery_FiltersTombstonedMatches(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	tomb := newInMemTombstone()
	svc := memory.NewService(f, tomb)

	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "secret"})
	require.NoError(t, err)
	f.SeedQueryResponse(lightrag.QueryResponse{
		References: []lightrag.ReferenceItem{
			{ReferenceID: m.ID, FilePath: "secret.txt"},
			{ReferenceID: "other-id", FilePath: "other.txt"},
		},
	})

	require.NoError(t, svc.DeleteMemory(ctx, m.ID))

	result, err := svc.Query(ctx, memory.Query{Mode: memory.QueryModeHybrid, Text: "q"})
	require.NoError(t, err)

	for _, match := range result.Matches {
		require.NotEqual(t, m.ID, match.ID,
			"Query must not return tombstoned memory %s (finding 5)", m.ID)
	}
}

func TestDescribe_FiltersTombstonedSources(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	tomb := newInMemTombstone()
	svc := memory.NewService(f, tomb)

	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "secret"})
	require.NoError(t, err)
	f.SeedQueryResponse(lightrag.QueryResponse{
		Response: "the answer",
		References: []lightrag.ReferenceItem{
			{ReferenceID: m.ID, FilePath: "secret.txt"},
			{ReferenceID: "alive-id", FilePath: "alive.txt"},
		},
	})

	require.NoError(t, svc.DeleteMemory(ctx, m.ID))

	result, err := svc.Describe(ctx, memory.Query{Mode: memory.QueryModeHybrid, Text: "q"})
	require.NoError(t, err)

	for _, src := range result.Sources {
		require.NotEqual(t, "secret.txt", src,
			"Describe must not include source from tombstoned memory (finding 5)")
	}
}

// --- Finding 7: GetMemory must pick deterministically from multi-doc tracks ---

func TestGetMemory_MultiDocTrack_Deterministic(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	svc := memory.NewService(f, nil)

	f.SeedMultiDocTrack("multi-track",
		lightrag.DocStatusResponse{ID: "doc-z", ContentSummary: "zzz", CreatedAt: "2024-01-15T12:00:00Z"},
		lightrag.DocStatusResponse{ID: "doc-a", ContentSummary: "aaa", CreatedAt: "2024-01-14T12:00:00Z"},
	)

	m1, err := svc.GetMemory(ctx, "multi-track")
	require.NoError(t, err)
	m2, err := svc.GetMemory(ctx, "multi-track")
	require.NoError(t, err)

	require.Equal(t, m1.Text, m2.Text,
		"GetMemory must return the same doc deterministically across calls (finding 7)")
}

// --- Finding 10: ListEdges dedup must not collapse distinct relations between same ordered pair ---

func TestListEdges_MultipleRelationsSamePair(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SeedEntity("a", nil)
	f.SeedEntity("b", nil)
	// Seed two graph edges with same (a,b) but different keywords via the multi-relation helper.
	f.SeedMultiRelationEdges("a", "b", "owns", "manages")

	svc := memory.NewService(f, nil)
	list, err := svc.ListEdges(ctx)
	require.NoError(t, err)

	var found []string
	for _, e := range list {
		if e.From == "a" && e.To == "b" {
			found = append(found, e.Relation)
		}
	}
	require.Contains(t, found, "owns", "first relation owns must be present (finding 10)")
	require.Contains(t, found, "manages", "second relation manages must be present (finding 10)")
}

// --- Finding 11: Tombstone rollback on non-transient DeleteDocs failure ---

func TestDeleteMemory_TombstoneRolledBackOnPermanentFailure(t *testing.T) {
	ctx := context.Background()
	inner := fake.New()
	lr := &deletionStatusFakeLR{inner: inner, status: "failure"}
	tomb := newInMemTombstone()
	svc := memory.NewService(lr, tomb)

	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "hello"})
	require.NoError(t, err)

	err = svc.DeleteMemory(ctx, m.ID)
	require.Error(t, err, "DeleteMemory must fail on non-success DeleteDocs status (finding 11)")

	deleted, terr := tomb.IsDeleted(ctx, m.ID)
	require.NoError(t, terr)
	require.False(t, deleted,
		"tombstone must be rolled back when DeleteDocs returns a permanent failure (finding 11)")
}
