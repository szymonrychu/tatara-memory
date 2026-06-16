package eval

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewClient(ClientConfig{
		BaseURL:      srv.URL,
		Token:        "test-token",
		Logger:       slog.New(slog.NewJSONHandler(io.Discard, nil)),
		PollInterval: time.Millisecond,
		JobTimeout:   time.Second,
	})
	require.NoError(t, err)
	return c
}

func TestNewClient_RequiresBaseURL(t *testing.T) {
	_, err := NewClient(ClientConfig{Token: "t"})
	require.Error(t, err)
}

func TestClient_BulkIngest_ReturnsJobIDAndSendsBearer(t *testing.T) {
	var gotAuth string
	var gotReq httpapi.BulkMemoriesRequest
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/memories:bulk", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		gotAuth = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotReq))
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(memory.IngestJob{ID: "job-1", Status: memory.JobStatusQueued})
	})

	id, err := c.BulkIngest(context.Background(), []SeedItem{{IdempotencyKey: "k", Text: "t"}})
	require.NoError(t, err)
	require.Equal(t, "job-1", id)
	require.Equal(t, "Bearer test-token", gotAuth)
	require.Len(t, gotReq.Items, 1)
	require.Equal(t, "k", gotReq.Items[0].IdempotencyKey)
}

func TestClient_BulkIngest_EmptyItems(t *testing.T) {
	c := testClient(t, func(http.ResponseWriter, *http.Request) { t.Fatal("should not call server") })
	_, err := c.BulkIngest(context.Background(), nil)
	require.Error(t, err)
}

func TestClient_BulkIngest_EmptyJobID(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(memory.IngestJob{Status: memory.JobStatusQueued})
	})
	_, err := c.BulkIngest(context.Background(), []SeedItem{{IdempotencyKey: "k", Text: "t"}})
	require.Error(t, err)
}

func TestClient_WaitJob_PollsToTerminal(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/ingest-jobs/job-1", r.URL.Path)
		status := memory.JobStatusRunning
		if calls.Add(1) >= 3 {
			status = memory.JobStatusSucceeded
		}
		_ = json.NewEncoder(w).Encode(memory.IngestJob{ID: "job-1", Status: status})
	})

	job, err := c.WaitJob(context.Background(), "job-1")
	require.NoError(t, err)
	require.Equal(t, memory.JobStatusSucceeded, job.Status)
	require.GreaterOrEqual(t, calls.Load(), int32(3))
}

func TestClient_WaitJob_Timeout(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(memory.IngestJob{ID: "job-1", Status: memory.JobStatusRunning})
	})
	c.jobTimeout = 5 * time.Millisecond
	_, err := c.WaitJob(context.Background(), "job-1")
	require.Error(t, err)
}

func TestClient_WaitJob_TerminalPartialAndFailed(t *testing.T) {
	for _, st := range []memory.JobStatus{memory.JobStatusFailed, memory.JobStatusPartial} {
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(memory.IngestJob{ID: "job-1", Status: st})
		})
		job, err := c.WaitJob(context.Background(), "job-1")
		require.NoError(t, err, "terminal status %q returns without error", st)
		require.Equal(t, st, job.Status)
	}
}

func TestClient_Query_DecodesMatches(t *testing.T) {
	var gotQ memory.Query
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/queries", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotQ))
		_ = json.NewEncoder(w).Encode(memory.QueryResult{Matches: []memory.QueryMatch{
			{ID: "m1", Text: "alpha"}, {ID: "m2", Text: "beta"},
		}})
	})

	res, err := c.Query(context.Background(), memory.Query{Mode: memory.QueryModeHybrid, Text: "q", TopK: 5})
	require.NoError(t, err)
	require.Len(t, res.Matches, 2)
	require.Equal(t, "alpha", res.Matches[0].Text)
	require.Equal(t, memory.QueryModeHybrid, gotQ.Mode)
	require.Equal(t, 5, gotQ.TopK)
}

func TestClient_Non2xxSurfacesError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	})
	_, err := c.Query(context.Background(), memory.Query{Mode: memory.QueryModeHybrid, Text: "q"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
}
