package memory_test

// Tests for obs-scaffold round-3 audit findings 1 and 6 in internal/memory.
// Finding 1: read ops (get, query, describe, get_entity, search_entities, list_edges)
//            must emit tatara_memory_op_total and an INFO log.
// Finding 6: DeleteMemoriesBySource must NOT emit a 'delete' counter for each inner
//            deleteMemoryRaw call on top of 'delete_by_source' (double-counting).

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// --- Finding 1: read ops emit tatara_memory_op_total ---

func TestService_ReadOpMetrics_GetMemory(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	reg := prometheus.NewRegistry()
	svc := memory.NewService(f, nil).WithMetrics(reg)

	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "hello"})
	require.NoError(t, err)

	_, err = svc.GetMemory(ctx, m.ID)
	require.NoError(t, err)

	mfs, _ := reg.Gather()
	require.InDelta(t, 1.0, memOpCounter(t, mfs, "get", "success"), 0.001,
		"GetMemory must increment 'get' success counter")
}

func TestService_ReadOpMetrics_GetMemory_InfoLog(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc := memory.NewService(f, nil).WithLogger(logger)

	m, _ := svc.CreateMemory(ctx, memory.Memory{Text: "hi"})
	_, _ = svc.GetMemory(ctx, m.ID)

	found := findObsLogLine(t, &buf, "memory.get")
	require.NotNil(t, found, "GetMemory must emit INFO log 'memory.get'")
	require.Equal(t, "get_memory", found["action"])
}

func TestService_ReadOpMetrics_Query(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	reg := prometheus.NewRegistry()
	svc := memory.NewService(f, nil).WithMetrics(reg)

	_, err := svc.Query(ctx, memory.Query{Mode: memory.QueryModeHybrid, Text: "x"})
	require.NoError(t, err)

	mfs, _ := reg.Gather()
	require.InDelta(t, 1.0, memOpCounter(t, mfs, "query", "success"), 0.001,
		"Query must increment 'query' success counter")
}

func TestService_ReadOpMetrics_SearchEntities(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SeedEntity("tatara", nil)
	reg := prometheus.NewRegistry()
	svc := memory.NewService(f, nil).WithMetrics(reg)

	_, err := svc.SearchEntities(ctx, "tatara")
	require.NoError(t, err)

	mfs, _ := reg.Gather()
	require.InDelta(t, 1.0, memOpCounter(t, mfs, "search_entities", "success"), 0.001,
		"SearchEntities must increment 'search_entities' success counter")
}

func TestService_ReadOpMetrics_GetEntity(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SeedEntity("tatara", nil)
	reg := prometheus.NewRegistry()
	svc := memory.NewService(f, nil).WithMetrics(reg)

	_, err := svc.GetEntity(ctx, "tatara")
	require.NoError(t, err)

	mfs, _ := reg.Gather()
	require.InDelta(t, 1.0, memOpCounter(t, mfs, "get_entity", "success"), 0.001,
		"GetEntity must increment 'get_entity' success counter")
}

func TestService_ReadOpMetrics_ListEdges(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SeedEntity("a", nil)
	f.SeedEntity("b", nil)
	reg := prometheus.NewRegistry()
	svc := memory.NewService(f, nil).WithMetrics(reg)

	_, err := svc.CreateEdge(ctx, memory.Edge{From: "a", To: "b", Relation: "rel"})
	require.NoError(t, err)

	_, err = svc.ListEdges(ctx)
	require.NoError(t, err)

	mfs, _ := reg.Gather()
	require.InDelta(t, 1.0, memOpCounter(t, mfs, "list_edges", "success"), 0.001,
		"ListEdges must increment 'list_edges' success counter")
}

// --- Finding 6: DeleteMemoriesBySource must not double-count 'delete' op ---

func TestService_DeleteBySource_NoDeleteDoubleCount(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	tomb := newInMemTombstone()
	src := newInMemSources()
	reg := prometheus.NewRegistry()

	svc := memory.NewServiceWithSources(f, tomb, src).WithMetrics(reg)

	m1, _ := svc.CreateMemory(ctx, memory.Memory{Text: "one"})
	m2, _ := svc.CreateMemory(ctx, memory.Memory{Text: "two"})
	_ = src.Add(ctx, "repo", "f.go", m1.ID)
	_ = src.Add(ctx, "repo", "f.go", m2.ID)

	n, err := svc.DeleteMemoriesBySource(ctx, "repo", "f.go")
	require.NoError(t, err)
	require.Equal(t, 2, n)

	mfs, _ := reg.Gather()

	// delete_by_source must be incremented exactly once.
	require.InDelta(t, 1.0, memOpCounter(t, mfs, "delete_by_source", "success"), 0.001,
		"delete_by_source must be incremented exactly once for the whole call")

	// 'delete' counter must be 0: the inner deleteMemoryRaw does not own the
	// 'delete' label; only the public DeleteMemory entry-point does.
	deleteCount := memOpCounter(t, mfs, "delete", "success")
	require.InDelta(t, 0.0, deleteCount, 0.001,
		"inner deleteMemoryRaw must not increment the 'delete' counter (finding 6: no double-counting)")
}

// --- helpers ---

func findObsLogLine(t *testing.T, buf *bytes.Buffer, msg string) map[string]any {
	t.Helper()
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var entry map[string]any
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry["msg"] == msg {
			return entry
		}
	}
	return nil
}
