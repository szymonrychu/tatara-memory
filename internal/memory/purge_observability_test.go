package memory_test

// Tests for issue #84: the internal purge path (delete_by_source /
// delete_by_sources) errored silently (no structured log) and its op-error
// metric was indistinguishable from user-facing ops, so an alert computed
// over the combined error ratio claimed user impact that never happened.
//
// Two things are asserted here:
//  1. tatara_memory_op_total carries a "class" label ("maintenance" for the
//     purge ops, "user" for everything else) so an alert can scope itself to
//     user-facing traffic only.
//  2. every purge failure path emits a structured log line (WARN for
//     backpressure, ERROR for everything else) instead of failing silently.
//
// The WARN class is memory.ErrBusy, not memory.ErrTransient. This branch was
// written before the taxonomy split in tatara-memory#97, when ErrTransient was
// the only non-permanent class and carried LightRAG's delete status="busy".
// #97 gave busy its own ErrBusy (429 + Retry-After) and reserved ErrTransient
// for genuine unavailability (upstream 5xx / unreachable, 503). Since the whole
// point of the split is "expected backpressure at WARN, real faults at ERROR",
// the predicate had to follow busy to ErrBusy - see MEMORY.md.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// --- test doubles ---

// deleteDocsOverride wraps the fake lightrag client but overrides DeleteDocs
// so tests can force the two upstream failure shapes DeleteMemoriesBySource
// must handle: a "busy" pipeline-lock response (backpressure), and a hard HTTP
// error status.
type deleteDocsOverride struct {
	*fake.Client
	mode string // "", "busy", "httperr"
}

func (w *deleteDocsOverride) DeleteDocs(ctx context.Context, req lightrag.DeleteDocRequest) (*lightrag.DeleteDocByIdResponse, error) {
	switch w.mode {
	case "busy":
		return &lightrag.DeleteDocByIdResponse{Status: "busy", Message: "pipeline busy"}, nil
	case "httperr":
		return nil, &lightrag.HTTPError{Status: 400, Path: "/documents/delete_document", Body: "bad request"}
	case "unavailable":
		return nil, &lightrag.HTTPError{Status: 502, Path: "/documents/delete_document", Body: "bad gateway"}
	default:
		return w.Client.DeleteDocs(ctx, req)
	}
}

// errSources is a sourceIndex test double whose TrackIDs/DeleteByFile calls
// can be made to fail on demand, to exercise DeleteMemoriesBySource's other
// two failure points (source-index lookup and source-index cleanup).
type errSources struct {
	ids           []string
	trackIDsErr   error
	deleteFileErr error
}

func (s *errSources) Add(_ context.Context, _, _, _ string) error { return nil }

func (s *errSources) TrackIDs(_ context.Context, _, _ string) ([]string, error) {
	if s.trackIDsErr != nil {
		return nil, s.trackIDsErr
	}
	return s.ids, nil
}

func (s *errSources) DeleteByFile(_ context.Context, _, _ string) (int64, error) {
	if s.deleteFileErr != nil {
		return 0, s.deleteFileErr
	}
	return int64(len(s.ids)), nil
}

// --- helpers ---

// findMetricLabel returns the value of labelName on the tatara_memory_op_total
// series matching op/result, and whether such a series was found at all.
func findMetricLabel(mfs []*dto.MetricFamily, op, result, labelName string) (string, bool) {
	for _, mf := range mfs {
		if mf.GetName() != "tatara_memory_op_total" {
			continue
		}
		for _, m := range mf.Metric {
			var opOK, resOK bool
			var labelVal string
			var labelFound bool
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "op" && lp.GetValue() == op {
					opOK = true
				}
				if lp.GetName() == "result" && lp.GetValue() == result {
					resOK = true
				}
				if lp.GetName() == labelName {
					labelVal = lp.GetValue()
					labelFound = true
				}
			}
			if opOK && resOK && labelFound {
				return labelVal, true
			}
		}
	}
	return "", false
}

func findLogLine(t *testing.T, buf *bytes.Buffer, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var entry map[string]any
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry["msg"] == msg {
			out = append(out, entry)
		}
	}
	return out
}

// --- metric class label ---

func TestOpMetric_DeleteBySource_ClassIsMaintenance(t *testing.T) {
	ctx := context.Background()
	lr := fake.New()
	tomb := newInMemTombstone()
	src := newInMemSources()
	reg := prometheus.NewRegistry()
	svc := memory.NewServiceWithSources(lr, tomb, src).WithMetrics(reg)

	m1, err := svc.CreateMemory(ctx, memory.Memory{Text: "one"})
	require.NoError(t, err)
	require.NoError(t, src.Add(ctx, "repoX", "a.go", m1.ID))

	_, err = svc.DeleteMemoriesBySource(ctx, "repoX", "a.go")
	require.NoError(t, err)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	class, found := findMetricLabel(mfs, "delete_by_source", "success", "class")
	require.True(t, found, "tatara_memory_op_total{op=delete_by_source} must carry a class label")
	require.Equal(t, "maintenance", class,
		"delete_by_source is an internal purge path, not user-facing traffic; an "+
			"op-error-ratio alert must be able to exclude it (issue #84)")
}

func TestOpMetric_DeleteBySources_ClassIsMaintenance(t *testing.T) {
	ctx := context.Background()
	lr := fake.New()
	tomb := newInMemTombstone()
	src := newInMemSources()
	reg := prometheus.NewRegistry()
	svc := memory.NewServiceWithSources(lr, tomb, src).WithMetrics(reg)

	_, err := svc.DeleteMemoriesBySources(ctx, "repoX", []string{"a.go"})
	require.NoError(t, err)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	class, found := findMetricLabel(mfs, "delete_by_sources", "success", "class")
	require.True(t, found, "tatara_memory_op_total{op=delete_by_sources} must carry a class label")
	require.Equal(t, "maintenance", class)
}

func TestOpMetric_UserFacingOps_ClassIsUser(t *testing.T) {
	ctx := context.Background()
	lr := fake.New()
	reg := prometheus.NewRegistry()
	svc := memory.NewService(lr, nil).WithMetrics(reg)

	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "hi"})
	require.NoError(t, err)
	_, err = svc.GetMemory(ctx, m.ID)
	require.NoError(t, err)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	class, found := findMetricLabel(mfs, "get", "success", "class")
	require.True(t, found)
	require.Equal(t, "user", class, "user-facing ops must keep the 'user' class")

	class, found = findMetricLabel(mfs, "create", "success", "class")
	require.True(t, found)
	require.Equal(t, "user", class)
}

// --- structured logging on purge failures ---

func TestDeleteMemoriesBySource_TrackIDsFailure_LogsError(t *testing.T) {
	ctx := context.Background()
	lr := fake.New()
	tomb := newInMemTombstone()
	src := &errSources{trackIDsErr: errors.New("pg: connection reset")}
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc := memory.NewServiceWithSources(lr, tomb, src).WithLogger(logger)

	_, err := svc.DeleteMemoriesBySource(ctx, "repoY", "bad.go")
	require.Error(t, err)

	lines := findLogLine(t, &buf, "memory.delete_by_source")
	require.NotEmpty(t, lines, "a source-index lookup failure in the purge path must be logged, not silent (issue #84)")
	entry := lines[0]
	require.Equal(t, "ERROR", entry["level"])
	require.Equal(t, "repoY", entry["repo"])
	require.Equal(t, "bad.go", entry["file_path"])
	require.NotEmpty(t, entry["error"])
}

// A held LightRAG pipeline lock is the one purge failure that is routine under
// load, so it must log at WARN rather than ERROR. Post-#97 that class is
// ErrBusy; this test asserted ErrTransient before the merge because that was
// then where status="busy" landed. Intentional change: the classification moved
// on main, the WARN intent did not.
func TestDeleteMemoriesBySource_BusyDeleteFailure_LogsWarn(t *testing.T) {
	ctx := context.Background()
	lr := &deleteDocsOverride{Client: fake.New()}
	tomb := newInMemTombstone()
	src := newInMemSources()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc := memory.NewServiceWithSources(lr, tomb, src).WithLogger(logger)

	m1, err := svc.CreateMemory(ctx, memory.Memory{Text: "one"})
	require.NoError(t, err)
	require.NoError(t, src.Add(ctx, "repoZ", "b.go", m1.ID))

	lr.mode = "busy"
	_, err = svc.DeleteMemoriesBySource(ctx, "repoZ", "b.go")
	require.Error(t, err)
	require.ErrorIs(t, err, memory.ErrBusy)
	require.NotErrorIs(t, err, memory.ErrTransient,
		"busy is backpressure (429), not unavailability (503) - tatara-memory#97")

	lines := findLogLine(t, &buf, "memory.delete_by_source")
	require.NotEmpty(t, lines, "a busy purge failure must still be logged")
	entry := lines[0]
	require.Equal(t, "WARN", entry["level"],
		"expected, retryable purge backpressure (LightRAG busy) should log at WARN, not ERROR")
	require.Equal(t, "repoZ", entry["repo"])
	require.Equal(t, "b.go", entry["file_path"])
}

func TestDeleteMemoriesBySource_HardDeleteFailure_LogsError(t *testing.T) {
	ctx := context.Background()
	lr := &deleteDocsOverride{Client: fake.New()}
	tomb := newInMemTombstone()
	src := newInMemSources()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc := memory.NewServiceWithSources(lr, tomb, src).WithLogger(logger)

	m1, err := svc.CreateMemory(ctx, memory.Memory{Text: "one"})
	require.NoError(t, err)
	require.NoError(t, src.Add(ctx, "repoW", "c.go", m1.ID))

	lr.mode = "httperr"
	_, err = svc.DeleteMemoriesBySource(ctx, "repoW", "c.go")
	require.Error(t, err)
	require.ErrorIs(t, err, memory.ErrUpstream)

	lines := findLogLine(t, &buf, "memory.delete_by_source")
	require.NotEmpty(t, lines, "a hard purge failure must be logged")
	entry := lines[0]
	require.Equal(t, "ERROR", entry["level"])
	require.Equal(t, "repoW", entry["repo"])
	require.Equal(t, "c.go", entry["file_path"])
}

// The other half of the post-#97 predicate: ErrTransient no longer means
// "retryable", it means the upstream is genuinely unavailable (5xx /
// unreachable, 503 on the wire). That is a real fault on the purge path and
// must reach ERROR, not be muted to WARN by the pre-split predicate.
func TestDeleteMemoriesBySource_UpstreamUnavailable_LogsError(t *testing.T) {
	ctx := context.Background()
	lr := &deleteDocsOverride{Client: fake.New()}
	tomb := newInMemTombstone()
	src := newInMemSources()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc := memory.NewServiceWithSources(lr, tomb, src).WithLogger(logger)

	m1, err := svc.CreateMemory(ctx, memory.Memory{Text: "one"})
	require.NoError(t, err)
	require.NoError(t, src.Add(ctx, "repoT", "f.go", m1.ID))

	lr.mode = "unavailable"
	_, err = svc.DeleteMemoriesBySource(ctx, "repoT", "f.go")
	require.Error(t, err)
	require.ErrorIs(t, err, memory.ErrTransient)

	lines := findLogLine(t, &buf, "memory.delete_by_source")
	require.NotEmpty(t, lines)
	require.Equal(t, "ERROR", lines[0]["level"],
		"post-#97 ErrTransient is genuine unavailability (503), not routine backpressure - it must not be downgraded to WARN")
}

func TestDeleteMemoriesBySource_SourceIndexCleanupFailure_LogsError(t *testing.T) {
	ctx := context.Background()
	lr := fake.New()
	tomb := newInMemTombstone()
	src := &errSources{deleteFileErr: errors.New("pg: deadlock detected")}
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc := memory.NewServiceWithSources(lr, tomb, src).WithLogger(logger)

	_, err := svc.DeleteMemoriesBySource(ctx, "repoV", "d.go")
	require.Error(t, err)

	lines := findLogLine(t, &buf, "memory.delete_by_source")
	require.NotEmpty(t, lines, "a source-index cleanup failure must be logged")
	entry := lines[0]
	require.Equal(t, "ERROR", entry["level"])
	require.Equal(t, "repoV", entry["repo"])
	require.Equal(t, "d.go", entry["file_path"])
}

func TestDeleteMemoriesBySources_PropagatedFailure_LogsError(t *testing.T) {
	ctx := context.Background()
	lr := fake.New()
	tomb := newInMemTombstone()
	src := &errSources{trackIDsErr: errors.New("pg: connection reset")}
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc := memory.NewServiceWithSources(lr, tomb, src).WithLogger(logger)

	_, err := svc.DeleteMemoriesBySources(ctx, "repoU", []string{"e.go"})
	require.Error(t, err)

	// The failure line keeps the ".incomplete" suffix introduced by the purge
	// budget work (tatara-memory#93) so it is distinguishable from the success
	// INFO line by message alone; the level is now chosen by logPurgeErr.
	lines := findLogLine(t, &buf, "memory.delete_by_sources.incomplete")
	require.NotEmpty(t, lines, "DeleteMemoriesBySources must also log when a per-file purge fails")
	entry := lines[0]
	require.Equal(t, "ERROR", entry["level"])
	require.Equal(t, "repoU", entry["repo"])
	require.NotEmpty(t, entry["error"])
	require.Contains(t, entry, "total_purged",
		"the partial purge count (tatara-memory#93) must survive on the failure line")
}

// The batch failure line follows the same taxonomy as the per-file one: a
// propagated ErrBusy is backpressure and logs WARN, so a wide reconcile that
// simply outruns the pipeline lock does not fill the log with ERRORs.
func TestDeleteMemoriesBySources_PropagatedBusy_LogsWarn(t *testing.T) {
	ctx := context.Background()
	lr := &deleteDocsOverride{Client: fake.New()}
	tomb := newInMemTombstone()
	src := newInMemSources()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc := memory.NewServiceWithSources(lr, tomb, src).WithLogger(logger)

	m1, err := svc.CreateMemory(ctx, memory.Memory{Text: "one"})
	require.NoError(t, err)
	require.NoError(t, src.Add(ctx, "repoS", "g.go", m1.ID))

	lr.mode = "busy"
	_, err = svc.DeleteMemoriesBySources(ctx, "repoS", []string{"g.go"})
	require.Error(t, err)
	require.ErrorIs(t, err, memory.ErrBusy)

	lines := findLogLine(t, &buf, "memory.delete_by_sources.incomplete")
	require.NotEmpty(t, lines)
	require.Equal(t, "WARN", lines[0]["level"])
}
