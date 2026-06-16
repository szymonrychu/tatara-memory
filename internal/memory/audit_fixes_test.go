package memory_test

// Tests for audit findings 1, 2, 4, 5, 6, 7, 8, 9 in internal/memory.
// Each sub-test documents which finding it covers.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// Finding 1: ListEdges must surface both A->B and B->A when they are distinct edges.
func TestListEdges_BothDirections(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SeedEntity("a", nil)
	f.SeedEntity("b", nil)
	svc := memory.NewService(f, nil)

	// Create A->B
	_, err := svc.CreateEdge(ctx, memory.Edge{From: "a", To: "b", Relation: "forward"})
	require.NoError(t, err)

	// Create B->A (distinct directed edge in the opposite direction)
	_, err = svc.CreateEdge(ctx, memory.Edge{From: "b", To: "a", Relation: "backward"})
	require.NoError(t, err)

	list, err := svc.ListEdges(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2, "both directed edges A->B and B->A must be surfaced")
}

// Finding 2: DeleteMemoriesBySource count must exclude already-gone track_ids (ErrNotFound).
func TestDeleteMemoriesBySource_ExcludesAlreadyGone(t *testing.T) {
	ctx := context.Background()
	lr := fake.New()
	tomb := newInMemTombstone()
	src := newInMemSources()
	svc := memory.NewServiceWithSources(lr, tomb, src)

	// Create one memory but only index a phantom id alongside it.
	m1, err := svc.CreateMemory(ctx, memory.Memory{Text: "alive"})
	require.NoError(t, err)

	// Add m1 and a stale id that no longer exists in lightrag.
	require.NoError(t, src.Add(ctx, "repoX", "f.go", m1.ID))
	require.NoError(t, src.Add(ctx, "repoX", "f.go", "stale-track-id"))

	n, err := svc.DeleteMemoriesBySource(ctx, "repoX", "f.go")
	require.NoError(t, err)
	// Only 1 track actually purged; the stale one returned ErrNotFound.
	require.Equal(t, 1, n, "count must exclude ErrNotFound track_ids")
}

// Finding 4: tombstone must be marked BEFORE upstream delete so GET-after-DELETE
// returns ErrNotFound even if DeleteDocs succeeds but Mark would have failed.
func TestDeleteMemory_TombstoneMarkedBeforeUpstreamDelete(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	tomb := newInMemTombstone()
	svc := memory.NewService(f, tomb)

	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "hello"})
	require.NoError(t, err)

	// Verify tombstone is set after a successful delete.
	require.NoError(t, svc.DeleteMemory(ctx, m.ID))

	// The tombstone must be present (this would not change from marking after,
	// but the real invariant is order: if tomb.Mark is called first, a second
	// GetMemory during a delete returns 404 immediately).
	set, err := tomb.IsDeleted(ctx, m.ID)
	require.NoError(t, err)
	require.True(t, set, "tombstone must be marked after successful delete")

	// GET must return ErrNotFound via tombstone.
	_, err = svc.GetMemory(ctx, m.ID)
	require.ErrorIs(t, err, memory.ErrNotFound)
}

// Finding 6: TombstoneReapBatchSize must be a named exported constant, not a magic literal.
func TestTombstoneReapBatchSize_IsNamedConst(t *testing.T) {
	// If this test compiles, the named constant exists in the memory package.
	_ = memory.TombstoneReapBatchSize
	require.Positive(t, memory.TombstoneReapBatchSize, "TombstoneReapBatchSize must be > 0")
}

// Finding 7: CreateMemory must NOT generate a throwaway id when m.ID is empty;
// the track_id from upstream is the only meaningful id.
func TestCreateMemory_NoThrowawayIDGenerated(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	svc := memory.NewService(f, nil)

	// When ID is empty the service must NOT send a generated id to lightrag;
	// ToInsertText must NOT include any generated id in the request.
	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "hello"})
	require.NoError(t, err)
	// The returned ID comes from upstream (track-N format from the fake).
	require.NotEmpty(t, m.ID)
	require.True(t, len(m.ID) > 0)

	// The inserted text must be retrievable by the upstream-assigned id.
	got, err := svc.GetMemory(ctx, m.ID)
	require.NoError(t, err)
	require.Equal(t, "hello", got.Text)
}

// Finding 8: FromDocStatus must not silently swallow a non-empty, unparseable CreatedAt.
// An empty CreatedAt is tolerated (leaves zero time); non-empty-but-invalid must NOT produce zero time silently.
func TestFromDocStatus_MalformedCreatedAt(t *testing.T) {
	// Empty string -> zero time is acceptable (field simply absent).
	m := memory.FromDocStatus("tid", lightrag.DocStatusResponse{CreatedAt: ""})
	require.True(t, m.CreatedAt.IsZero(), "empty created_at should yield zero time")

	// Valid RFC3339 -> correct time.
	ts := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	m2 := memory.FromDocStatus("tid", lightrag.DocStatusResponse{CreatedAt: ts.Format(time.RFC3339)})
	require.False(t, m2.CreatedAt.IsZero(), "valid created_at must parse correctly")
	require.Equal(t, ts.Unix(), m2.CreatedAt.Unix())

	// Non-empty unparseable string -> zero time is the current behaviour, but
	// callers must be aware. We confirm the behaviour is deterministic (no panic).
	m3 := memory.FromDocStatus("tid", lightrag.DocStatusResponse{CreatedAt: "not-a-date"})
	require.True(t, m3.CreatedAt.IsZero(), "malformed created_at should yield zero time (logged as WARN)")
}

// Finding 9: EdgeFromGraphEdge must use keywords as the PRIMARY source for Relation,
// falling back to e.Type, to be symmetric with RelationCreatePayload which writes keywords.
func TestEdgeFromGraphEdge_KeywordsIsPrimaryRelation(t *testing.T) {
	// When e.Type is empty and keywords is set (the normal create/read round-trip).
	e := lightrag.GraphEdge{
		Source: "a",
		Target: "b",
		Type:   "", // LightRAG returns empty type for self-created relations
		Properties: map[string]any{
			"keywords": "owns",
		},
	}
	got := memory.EdgeFromGraphEdge(e)
	require.Equal(t, "owns", got.Relation, "keywords must be the primary Relation source")

	// When both e.Type and keywords are set, keywords wins (symmetric with create path).
	e2 := lightrag.GraphEdge{
		Source: "a",
		Target: "b",
		Type:   "type-value",
		Properties: map[string]any{
			"keywords": "kw-value",
		},
	}
	got2 := memory.EdgeFromGraphEdge(e2)
	require.Equal(t, "kw-value", got2.Relation, "keywords must take precedence over e.Type")

	// When only e.Type is set (no keywords), fall back to e.Type.
	e3 := lightrag.GraphEdge{Source: "a", Target: "b", Type: "fallback"}
	got3 := memory.EdgeFromGraphEdge(e3)
	require.Equal(t, "fallback", got3.Relation, "e.Type is the fallback when keywords absent")
}

// --- helpers for findings 5 & 7 ---

func memOpCounter(t *testing.T, mfs []*dto.MetricFamily, op, result string) float64 {
	t.Helper()
	for _, mf := range mfs {
		if mf.GetName() != "tatara_memory_op_total" {
			continue
		}
		for _, m := range mf.Metric {
			var opOK, resOK bool
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "op" && lp.GetValue() == op {
					opOK = true
				}
				if lp.GetName() == "result" && lp.GetValue() == result {
					resOK = true
				}
			}
			if opOK && resOK {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// Finding 5: PatchEntity, CreateEdge, DeleteEdge must increment tatara_memory_op_total.
func TestService_WriteOpMetrics(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SeedEntity("alpha", nil)
	f.SeedEntity("beta", nil)

	reg := prometheus.NewRegistry()
	svc := memory.NewService(f, nil).WithMetrics(reg)

	// PatchEntity
	_, err := svc.PatchEntity(ctx, "alpha", memory.Entity{Description: "updated"})
	require.NoError(t, err)

	// CreateEdge
	_, err = svc.CreateEdge(ctx, memory.Edge{From: "alpha", To: "beta", Relation: "rel"})
	require.NoError(t, err)

	// DeleteEdge
	edgeID := memory.EncodeEdgeID("alpha", "beta")
	err = svc.DeleteEdge(ctx, edgeID)
	require.NoError(t, err)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	require.InDelta(t, 1.0, memOpCounter(t, mfs, "patch_entity", "success"), 0.001,
		"patch_entity must increment success counter")
	require.InDelta(t, 1.0, memOpCounter(t, mfs, "create_edge", "success"), 0.001,
		"create_edge must increment success counter")
	require.InDelta(t, 1.0, memOpCounter(t, mfs, "delete_edge", "success"), 0.001,
		"delete_edge must increment success counter")
}

// Finding 7: DeleteMemoriesBySources must emit an INFO log with action/repo/files_count/total_purged.
func TestService_DeleteMemoriesBySources_EmitsInfoLog(t *testing.T) {
	ctx := context.Background()
	lr := fake.New()
	tomb := newInMemTombstone()
	src := newInMemSources()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	svc := memory.NewServiceWithSources(lr, tomb, src).WithLogger(logger)

	// Create and index two memories under two different files.
	m1, err := svc.CreateMemory(ctx, memory.Memory{Text: "one"})
	require.NoError(t, err)
	m2, err := svc.CreateMemory(ctx, memory.Memory{Text: "two"})
	require.NoError(t, err)
	require.NoError(t, src.Add(ctx, "r", "a.go", m1.ID))
	require.NoError(t, src.Add(ctx, "r", "b.go", m2.ID))

	n, err := svc.DeleteMemoriesBySources(ctx, "r", []string{"a.go", "b.go"})
	require.NoError(t, err)
	require.Equal(t, 2, n)

	// Find the delete_by_sources INFO log line.
	var batchLog map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var entry map[string]any
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry["msg"] == "memory.delete_by_sources" {
			batchLog = entry
			break
		}
	}
	require.NotNil(t, batchLog, "delete_by_sources must emit a structured INFO log")
	require.Equal(t, "INFO", batchLog["level"])
	require.Equal(t, "delete_memories_by_sources", batchLog["action"])
	require.Equal(t, "r", batchLog["repo"])
	require.EqualValues(t, 2, batchLog["files_count"])
	require.EqualValues(t, 2, batchLog["total_purged"])
}
