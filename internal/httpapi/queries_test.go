package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type queryStub struct {
	stubService
	qres memory.QueryResult
	dres memory.DescribeResult
	qerr error
}

func (q *queryStub) Query(_ context.Context, _ memory.Query) (memory.QueryResult, error) {
	return q.qres, q.qerr
}

func (q *queryStub) QueryData(_ context.Context, _ memory.Query) (memory.QueryResult, error) {
	return q.qres, q.qerr
}

func (q *queryStub) Describe(_ context.Context, _ memory.Query) (memory.DescribeResult, error) {
	return q.dres, q.qerr
}

func TestPostQuery200(t *testing.T) {
	svc := &queryStub{qres: memory.QueryResult{Matches: []memory.QueryMatch{{ID: "m1", Score: 0.9}}}}
	srv := newSrv(t, svc)
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mode": "hybrid", "text": "alpha"})
	resp, err := http.Post(srv.URL+"/queries", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)

	var got memory.QueryResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got.Matches, 1)
}

func TestPostQueryInvalidMode400(t *testing.T) {
	srv := newSrv(t, &queryStub{})
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mode": "nope", "text": "x"})
	resp, err := http.Post(srv.URL+"/queries", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 400, resp.StatusCode)
}

func TestPostQueriesDescribe200(t *testing.T) {
	svc := &queryStub{dres: memory.DescribeResult{Response: "answer"}}
	srv := newSrv(t, svc)
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mode": "hybrid", "text": "x"})
	resp, err := http.Post(srv.URL+"/queries:describe", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)
}

func TestPostQueriesData200(t *testing.T) {
	svc := &queryStub{qres: memory.QueryResult{Matches: []memory.QueryMatch{
		{ID: "c1", Score: 1.0}, {ID: "c2", Score: 0.5},
	}}}
	srv := newSrv(t, svc)
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mode": "hybrid", "text": "x"})
	resp, err := http.Post(srv.URL+"/queries:data", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)

	var got memory.QueryResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got.Matches, 2)
	require.InDelta(t, 1.0, got.Matches[0].Score, 1e-9, "scored matches are returned")
}

func TestPostQueriesDataInvalidMode400(t *testing.T) {
	srv := newSrv(t, &queryStub{})
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mode": "nope", "text": "x"})
	resp, err := http.Post(srv.URL+"/queries:data", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 400, resp.StatusCode)
}

// TestPostQueryUnknownField400 ensures that a body containing a valid mode but
// an unrecognised extra field is rejected with 400 rather than silently
// accepted. Without DisallowUnknownFields the extra field is dropped and the
// request succeeds (200); with it the decoder returns an error immediately.
func TestPostQueryUnknownField400(t *testing.T) {
	svc := &queryStub{qres: memory.QueryResult{Matches: []memory.QueryMatch{{ID: "m1", Score: 0.9}}}}
	srv := newSrv(t, svc)
	defer srv.Close()

	// mode is valid, text is present, but "typo_field" is unknown.
	body, _ := json.Marshal(map[string]string{"mode": "hybrid", "text": "x", "typo_field": "oops"})
	resp, err := http.Post(srv.URL+"/queries", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestPostQueryDescribeUnknownField400 is the same check on /queries:describe.
func TestPostQueryDescribeUnknownField400(t *testing.T) {
	svc := &queryStub{dres: memory.DescribeResult{Response: "answer"}}
	srv := newSrv(t, svc)
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mode": "hybrid", "text": "x", "typo_field": "oops"})
	resp, err := http.Post(srv.URL+"/queries:describe", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestPostQueryBodySizeLimit413 ensures /queries rejects bodies that exceed
// the per-endpoint size limit rather than reading unbounded input into memory.
func TestPostQueryBodySizeLimit413(t *testing.T) {
	srv := newSrv(t, &queryStub{})
	defer srv.Close()

	// Build a body larger than the 1 MiB limit for small POST endpoints.
	huge := `{"mode":"hybrid","text":"` + strings.Repeat("x", 2<<20) + `"}`
	resp, err := http.Post(srv.URL+"/queries", "application/json", strings.NewReader(huge))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
