package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
