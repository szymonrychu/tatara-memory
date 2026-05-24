package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/auth/testjwks"
	"github.com/szymonrychu/tatara-memory/internal/httpapi"
)

func TestE2EAllEndpointsAuthEnforced(t *testing.T) {
	tj := testjwks.Start(t)
	defer tj.Close()

	r := httpapi.NewRouter(httpapi.Config{
		Service: &queryStub{},
		Ingest:  &ingestStub{},
		Verify:  tj.Middleware("tatara-memory"),
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	endpoints := []struct {
		method, path string
		body         []byte
	}{
		{"POST", "/v1/memories", []byte(`{"text":"x"}`)},
		{"GET", "/v1/memories/m1", nil},
		{"DELETE", "/v1/memories/m1", nil},
		{"POST", "/v1/memories:bulk", []byte(`{"items":[{"text":"a"}]}`)},
		{"GET", "/v1/ingest-jobs/j1", nil},
		{"POST", "/v1/queries", []byte(`{"mode":"hybrid","text":"x"}`)},
		{"POST", "/v1/queries:describe", []byte(`{"mode":"hybrid","text":"x"}`)},
		{"GET", "/v1/entities/e1", nil},
		{"GET", "/v1/entities?q=t", nil},
		{"PATCH", "/v1/entities/e1", []byte(`{"description":"d"}`)},
		{"GET", "/v1/edges", nil},
		{"POST", "/v1/edges", []byte(`{"from_entity":"a","to_entity":"b","relation":"r"}`)},
		{"DELETE", "/v1/edges/e1", nil},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req, _ := http.NewRequest(ep.method, srv.URL+ep.path, bytes.NewReader(ep.body))
			if ep.body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "expected 401 without token")

			// The auth middleware (from internal/auth) writes plain text, not JSON envelope.
			// The e2e assertion only checks status code; content-type and JSON envelope
			// are verified in per-handler unit tests where our own middleware stack fires first.
			_ = json.NewDecoder(resp.Body)
		})
	}
}
