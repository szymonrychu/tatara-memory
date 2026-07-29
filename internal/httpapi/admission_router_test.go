package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// blockingIngest parks Enqueue until release is closed, holding the
// /memories:bulk admission slot.
type blockingIngest struct {
	entered chan struct{}
	release chan struct{}
}

func (s *blockingIngest) Enqueue(_ context.Context, _ []memory.IngestItem) (memory.IngestJob, error) {
	s.entered <- struct{}{}
	<-s.release
	return memory.IngestJob{ID: "job1", Status: memory.JobStatusQueued}, nil
}

func (s *blockingIngest) GetJob(_ context.Context, _ string) (memory.IngestJob, error) {
	return memory.IngestJob{}, nil
}

// blockingCodeGraph parks Push until release is closed, holding the
// /code-graph:bulk admission slot. Only Push is exercised; every other method
// is the embedded nil interface and would panic if called.
type blockingCodeGraph struct {
	httpapi.CodeGraphService
	entered chan struct{}
	release chan struct{}
}

func (s *blockingCodeGraph) Push(_ context.Context, _ codegraph.GraphPush) (codegraph.PushResult, error) {
	s.entered <- struct{}{}
	<-s.release
	return codegraph.PushResult{}, nil
}

// Both bulk write routes are the ones that consume the shared Postgres pool
// under a concurrent-ingest burst (tatara-memory#79/#80/#82/#87), so both must
// be admission controlled end to end through the real router and middleware
// stack - and a saturated bulk route must not affect anything else.
func TestRouter_BulkRoutesShed429WhenSaturated(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	ing := &blockingIngest{entered: make(chan struct{}, 1), release: release}
	cg := &blockingCodeGraph{entered: make(chan struct{}, 1), release: release}

	r := httpapi.NewRouter(httpapi.Config{
		Service:                  &stubService{},
		Ingest:                   ing,
		CodeGraph:                cg,
		MemoriesBulkMaxInFlight:  1,
		CodeGraphBulkMaxInFlight: 1,
		AdmissionWait:            10 * time.Millisecond,
		AdmissionRetryAfter:      3 * time.Second,
	})

	cases := []struct {
		name    string
		path    string
		body    string
		entered chan struct{}
	}{
		{"memories bulk", "/memories:bulk", `{"items":[{"text":"a"}]}`, ing.entered},
		{"code graph bulk", "/code-graph:bulk", `{"repo":"r","files":["a.go"]}`, cg.entered},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			go r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(c.body)))
			select {
			case <-c.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("first request never reached the handler")
			}

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(c.body)))
			require.Equal(t, http.StatusTooManyRequests, w.Code,
				"%s must shed with 429 when its admission budget is saturated, not 503", c.path)
			require.NotEmpty(t, w.Header().Get("Retry-After"))
		})
	}

	// The two bulk classes hold separate budgets and neither blocks the rest of
	// the API: a saturated bulk route must not make the service look down.
	wr := httptest.NewRecorder()
	r.ServeHTTP(wr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, wr.Code)
}
