package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
)

type queryStub struct {
	stubService
	qres httpapi.QueryResult
	dres httpapi.DescribeResult
	qerr error
}

func (q *queryStub) Query(_ context.Context, _ httpapi.Query) (httpapi.QueryResult, error) {
	return q.qres, q.qerr
}

func (q *queryStub) Describe(_ context.Context, _ httpapi.Query) (httpapi.DescribeResult, error) {
	return q.dres, q.qerr
}

func TestPostQuery200(t *testing.T) {
	svc := &queryStub{qres: httpapi.QueryResult{Matches: []httpapi.QueryMatch{{ID: "m1", Score: 0.9}}}}
	srv := newSrv(t, svc)
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mode": "hybrid", "text": "alpha"})
	resp, err := http.Post(srv.URL+"/v1/queries", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)

	var got httpapi.QueryResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got.Matches, 1)
}

func TestPostQueryInvalidMode400(t *testing.T) {
	srv := newSrv(t, &queryStub{})
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mode": "nope", "text": "x"})
	resp, err := http.Post(srv.URL+"/v1/queries", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 400, resp.StatusCode)
}

func TestPostQueriesDescribe200(t *testing.T) {
	svc := &queryStub{dres: httpapi.DescribeResult{Response: "answer"}}
	srv := newSrv(t, svc)
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mode": "hybrid", "text": "x"})
	resp, err := http.Post(srv.URL+"/v1/queries:describe", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)
}
