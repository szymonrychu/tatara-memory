package httpapi_test

// End-to-end proof of the #90/#91 <-> #97 reconciliation: an exhausted
// whole-batch purge budget must reach the client as 429 + Retry-After, not 503.
//
// #93 (this branch) was written before memory.ErrBusy existed and mapped
// *lightrag.BusyError to ErrTransient, which was the only non-permanent
// classification at the time (it existed purely to escape ErrUpstream -> 502).
// #97 then split the taxonomy and moved the single-response `status="busy"`
// envelope to ErrBusy -> 429. Leaving the exhausted-budget path on ErrTransient
// would mean one busy response returns 429 while 45 seconds of the same busy
// response returns 503 - the longer the backpressure, the more it looks like an
// outage. This test walks the real router, the real memory.Service and the real
// errmap, so nothing between them can silently regress that.

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// budgetExhaustedLR behaves like the real HTTP client once LightRAG has kept the
// pipeline lock for the whole busy-retry budget: every DeleteDocs fails with a
// typed *lightrag.BusyError. Everything else (InsertText, TrackStatus) stays on
// the fake so the memory under test is genuinely indexed and deletable.
type budgetExhaustedLR struct {
	*fake.Client
}

func (b *budgetExhaustedLR) DeleteDocs(_ context.Context, _ lightrag.DeleteDocRequest) (*lightrag.DeleteDocByIdResponse, error) {
	return nil, &lightrag.BusyError{Op: lightrag.OpDeleteDocs, Attempts: 12, Waited: 45 * time.Second}
}

// srcIndex is the minimal source index memory.Service needs to find the
// track_ids a reconcile file produced.
type srcIndex struct {
	mu  sync.Mutex
	idx map[string][]string
}

func (s *srcIndex) add(repo, file, trackID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idx[repo+"|"+file] = append(s.idx[repo+"|"+file], trackID)
}

func (s *srcIndex) TrackIDs(_ context.Context, repo, file string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.idx[repo+"|"+file]...), nil
}

func (s *srcIndex) DeleteByFile(_ context.Context, repo, file string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := repo + "|" + file
	n := int64(len(s.idx[k]))
	delete(s.idx, k)
	return n, nil
}

func TestBulkIngestReconcile_ExhaustedPurgeBudgetIs429WithRetryAfter(t *testing.T) {
	ctx := context.Background()
	src := &srcIndex{idx: map[string][]string{}}
	lr := &budgetExhaustedLR{Client: fake.New()}
	svc := memory.NewServiceWithSources(lr, nil, src)

	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "indexed from gone.go"})
	require.NoError(t, err)
	src.add("repoZ", "gone.go", m.ID)

	srv := newSrvIngest(t, svc, &ingestStub{})
	defer srv.Close()

	body := `{"repo":"repoZ","reconcile_files":["gone.go"]}`
	resp, err := http.Post(srv.URL+"/memories:bulk", "application/json", bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode,
		"an exhausted purge budget is backpressure: LightRAG answered HTTP 200 throughout and "+
			"the work is still doable, so the caller must be told to come back (429), not that "+
			"the service is unavailable (503)")
	require.NotEqual(t, http.StatusServiceUnavailable, resp.StatusCode,
		"503 is reserved for genuine unavailability worth alerting on (tatara-memory#80)")

	ra := resp.Header.Get("Retry-After")
	require.NotEmpty(t, ra, "429 must tell the caller when to come back; both clients honour it "+
		"(tatara-memory-repo-ingester#32, tatara-operator#484)")
	secs, err := strconv.Atoi(ra)
	require.NoError(t, err)
	require.Positive(t, secs)
}
