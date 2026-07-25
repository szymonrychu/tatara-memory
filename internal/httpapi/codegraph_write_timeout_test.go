package httpapi_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// slowCodeGraph.Push sleeps for delay (or returns ctx.Err() if the request
// context is cancelled first) before succeeding. Only Push is exercised by
// this file's tests; every other CodeGraphService method is the embedded nil
// interface and would panic if called.
type slowCodeGraph struct {
	httpapi.CodeGraphService
	delay time.Duration
}

func (s *slowCodeGraph) Push(ctx context.Context, p codegraph.GraphPush) (codegraph.PushResult, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return codegraph.PushResult{}, ctx.Err()
	}
	return codegraph.PushResult{Repo: p.Repo}, nil
}

// slowMemoryService.CreateMemory sleeps for delay before succeeding. Used
// only as the negative control below; every other MemoryService method is
// the embedded nil interface.
type slowMemoryService struct {
	httpapi.MemoryService
	delay time.Duration
}

func (s *slowMemoryService) CreateMemory(ctx context.Context, m memory.Memory) (memory.Memory, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return memory.Memory{}, ctx.Err()
	}
	m.ID = "mem_slow"
	return m, nil
}

// TestPostCodeGraph_SurvivesServerWriteTimeout proves the fix for review
// finding "Important 1" on tatara-memory#85/#86: handlePostCodeGraph clears
// the connection's write deadline (internal/httpapi/codegraph.go), so a push
// that legitimately runs past http.Server's WriteTimeout still delivers its
// response instead of having it silently dropped - which would otherwise
// cause the ingest client to retry an already-successful push and triple the
// load on the shared Postgres pool the whole fix exists to protect.
func TestPostCodeGraph_SurvivesServerWriteTimeout(t *testing.T) {
	cg := &slowCodeGraph{delay: 150 * time.Millisecond}
	router := httpapi.NewRouter(httpapi.Config{Service: &stubService{}, CodeGraph: cg})

	srv := httptest.NewUnstartedServer(router)
	srv.Config.WriteTimeout = 50 * time.Millisecond // far shorter than cg.delay
	srv.Start()
	defer srv.Close()

	body := []byte(`{"repo":"r","files":[]}`)
	start := time.Now()
	resp, err := http.Post(srv.URL+"/code-graph:bulk", "application/json", bytes.NewReader(body))
	require.NoError(t, err, "response must still arrive despite the short server WriteTimeout")
	defer func() { _ = resp.Body.Close() }()
	elapsed := time.Since(start)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.GreaterOrEqual(t, elapsed, cg.delay,
		"response only arrives after Push's full delay, proving the write deadline did not cut the handler off early")
}

// TestControlRoute_LosesResponseToServerWriteTimeout is the negative control
// for the test above: a route that does NOT clear its write deadline loses
// its response once a short WriteTimeout elapses, even though the handler
// itself succeeds. This proves the test harness is meaningful - a short
// WriteTimeout genuinely breaks slow-but-successful responses here - so the
// pass above is evidence the codegraph exemption works, not that the
// scenario was toothless to begin with. It also documents, concretely, the
// "silent partial success -> client retries -> pool pressure" failure mode
// that motivated exempting /code-graph:bulk in the first place.
func TestControlRoute_LosesResponseToServerWriteTimeout(t *testing.T) {
	svc := &slowMemoryService{delay: 150 * time.Millisecond}
	router := httpapi.NewRouter(httpapi.Config{Service: svc})

	srv := httptest.NewUnstartedServer(router)
	srv.Config.WriteTimeout = 50 * time.Millisecond
	srv.Start()
	defer srv.Close()

	body := []byte(`{"text":"x"}`)
	resp, err := http.Post(srv.URL+"/memories", "application/json", bytes.NewReader(body))
	if err == nil {
		defer func() { _ = resp.Body.Close() }()
	}
	require.Error(t, err,
		"a route that does not clear its write deadline must lose its response to a short WriteTimeout - this is what codegraph.go's exemption prevents")
}
