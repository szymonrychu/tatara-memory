package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type ingestStub struct {
	enq memory.IngestJob
	job memory.IngestJob
	err error
}

func (s *ingestStub) Enqueue(_ context.Context, _ []memory.IngestItem) (memory.IngestJob, error) {
	return s.enq, s.err
}
func (s *ingestStub) GetJob(_ context.Context, _ string) (memory.IngestJob, error) {
	return s.job, s.err
}

func newSrvIngest(t *testing.T, svc httpapi.MemoryService, ing httpapi.IngestService) *httptest.Server {
	t.Helper()
	return httptest.NewServer(httpapi.NewRouter(httpapi.Config{Service: svc, Ingest: ing}))
}

func TestBulkIngest202(t *testing.T) {
	ing := &ingestStub{enq: memory.IngestJob{ID: "job1", Status: memory.JobStatusQueued}}
	srv := newSrvIngest(t, &stubService{}, ing)
	defer srv.Close()

	body, _ := json.Marshal(map[string]interface{}{
		"items": []map[string]string{{"text": "a"}, {"text": "b"}},
	})
	resp, err := http.Post(srv.URL+"/memories:bulk", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	var got memory.IngestJob
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, "job1", got.ID)
}

func TestBulkIngestEmpty400(t *testing.T) {
	srv := newSrvIngest(t, &stubService{}, &ingestStub{err: errors.New("ingest: empty items")})
	defer srv.Close()

	body, _ := json.Marshal(map[string]interface{}{"items": []map[string]string{}})
	resp, err := http.Post(srv.URL+"/memories:bulk", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 400, resp.StatusCode)
}

func TestGetJob200(t *testing.T) {
	ing := &ingestStub{job: memory.IngestJob{ID: "j1", Status: memory.JobStatusRunning, Total: 5, Done: 2}}
	srv := newSrvIngest(t, &stubService{}, ing)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ingest-jobs/j1")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)
}

// reconcileSpyService records DeleteMemoriesBySource calls and embeds stubService.
type reconcileSpyService struct {
	stubService
	mu      sync.Mutex
	deleted [][2]string // {repo, file}
}

func (s *reconcileSpyService) DeleteMemoriesBySource(_ context.Context, repo, file string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, [2]string{repo, file})
	return 0, nil
}

func (s *reconcileSpyService) snapshot() [][2]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][2]string(nil), s.deleted...)
}

func TestBulkIngestBareArrayBackCompat(t *testing.T) {
	ing := &ingestStub{enq: memory.IngestJob{ID: "jobBC", Status: memory.JobStatusQueued}}
	srv := newSrvIngest(t, &reconcileSpyService{}, ing)
	defer srv.Close()

	// Legacy bare array body must still be accepted.
	body := `[{"text":"a"},{"text":"b"}]`
	resp, err := http.Post(srv.URL+"/memories:bulk", "application/json", bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
}

func TestBulkIngestReconcileFilesPurgesFirst(t *testing.T) {
	spy := &reconcileSpyService{}
	ing := &ingestStub{enq: memory.IngestJob{ID: "jobRC", Status: memory.JobStatusQueued}}
	srv := newSrvIngest(t, spy, ing)
	defer srv.Close()

	body := `{"reconcile_files":["a.go","b.go"],
		"items":[{"text":"new a","metadata":{"repo":"repoA","file_path":"a.go"}}]}`
	resp, err := http.Post(srv.URL+"/memories:bulk", "application/json", bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	got := spy.snapshot()
	require.ElementsMatch(t, [][2]string{{"repoA", "a.go"}, {"repoA", "b.go"}}, got)
}

// TestBulkIngestReconcileFilesTopLevelRepo exercises the explicit repo field path,
// including the pure-deletion case (reconcile_files set, no items).
func TestBulkIngestReconcileFilesTopLevelRepo(t *testing.T) {
	spy := &reconcileSpyService{}
	ing := &ingestStub{enq: memory.IngestJob{ID: "jobTL", Status: memory.JobStatusQueued}}
	srv := newSrvIngest(t, spy, ing)
	defer srv.Close()

	// reconcile_files with explicit repo field and items.
	body := `{"repo":"repoB","reconcile_files":["x.go","y.go"],
		"items":[{"text":"new x","metadata":{"repo":"repoB","file_path":"x.go"}}]}`
	resp, err := http.Post(srv.URL+"/memories:bulk", "application/json", bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.ElementsMatch(t, [][2]string{{"repoB", "x.go"}, {"repoB", "y.go"}}, spy.snapshot())
}

// TestBulkIngestPureDeletion exercises the pure-deletion reconcile path: files
// deleted from a repo, no items to insert. Prior to the fix, repo could not be
// derived and the purge was silently skipped.
func TestBulkIngestPureDeletion(t *testing.T) {
	spy := &reconcileSpyService{}
	ing := &ingestStub{}
	srv := newSrvIngest(t, spy, ing)
	defer srv.Close()

	body := `{"repo":"repoC","reconcile_files":["gone.go"]}`
	resp, err := http.Post(srv.URL+"/memories:bulk", "application/json", bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	got := spy.snapshot()
	require.ElementsMatch(t, [][2]string{{"repoC", "gone.go"}}, got)
}

// TestBulkIngestReconcileFilesNoRepoReturns400 ensures that callers who send
// reconcile_files without either a top-level repo field or item metadata get a
// 400 rather than a silent no-op.
func TestBulkIngestReconcileFilesNoRepoReturns400(t *testing.T) {
	spy := &reconcileSpyService{}
	ing := &ingestStub{}
	srv := newSrvIngest(t, spy, ing)
	defer srv.Close()

	body := `{"reconcile_files":["gone.go"]}`
	resp, err := http.Post(srv.URL+"/memories:bulk", "application/json", bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, spy.snapshot())
}

// countingReconcileSvc returns a configurable purge count for DeleteMemoriesBySource.
type countingReconcileSvc struct {
	stubService
	mu      sync.Mutex
	deleted [][2]string
	counts  map[string]int // file -> purge count
}

func (s *countingReconcileSvc) DeleteMemoriesBySource(_ context.Context, repo, file string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, [2]string{repo, file})
	if s.counts != nil {
		return s.counts[file], nil
	}
	return 3, nil // default: 3 memories purged
}

// TestBulkIngestReconcile_LogsPurgeCount verifies that the reconcile purge INFO
// log captures the deleted count (finding 2). A non-zero count must be accepted
// and the request must still return 202.
func TestBulkIngestReconcile_LogsPurgeCount(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	spy := &countingReconcileSvc{counts: map[string]int{"a.go": 5}}
	ing := &ingestStub{enq: memory.IngestJob{ID: "j-log", Status: memory.JobStatusQueued}}
	r := httpapi.NewRouter(httpapi.Config{Service: spy, Ingest: ing, Logger: logger})
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := `{"repo":"myrepo","reconcile_files":["a.go"],"items":[{"text":"x","metadata":{"repo":"myrepo","file_path":"a.go"}}]}`
	resp, err := http.Post(srv.URL+"/memories:bulk", "application/json", bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	// Parse JSON log lines and find the reconcile purge entry.
	var purgeLine map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		if m["msg"] == "memories.reconcile.purge" {
			purgeLine = m
			break
		}
	}
	require.NotNil(t, purgeLine, "reconcile purge INFO log not emitted")
	require.EqualValues(t, 5, purgeLine["deleted"], "purge count must reflect returned count")
	require.Equal(t, "myrepo", purgeLine["repo"])
	require.EqualValues(t, 1, purgeLine["files"])
}
