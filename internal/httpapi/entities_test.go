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

type entStub struct {
	stubService
	e httpapi.Entity
	q []httpapi.Entity
}

func (s *entStub) GetEntity(_ context.Context, _ string) (httpapi.Entity, error) { return s.e, nil }
func (s *entStub) SearchEntities(_ context.Context, _ string) ([]httpapi.Entity, error) {
	return s.q, nil
}
func (s *entStub) PatchEntity(_ context.Context, _ string, p httpapi.Entity) (httpapi.Entity, error) {
	return p, nil
}

func TestGetEntity200(t *testing.T) {
	srv := newSrv(t, &entStub{e: httpapi.Entity{ID: "e1", Name: "tatara"}})
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/entities/e1")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)
}

func TestSearchEntities200(t *testing.T) {
	srv := newSrv(t, &entStub{q: []httpapi.Entity{{ID: "e1"}}})
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/entities?q=tat")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)
}

func TestSearchEntitiesMissingQ400(t *testing.T) {
	srv := newSrv(t, &entStub{})
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/entities")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 400, resp.StatusCode)
}

func TestPatchEntity200(t *testing.T) {
	srv := newSrv(t, &entStub{})
	defer srv.Close()
	body, _ := json.Marshal(httpapi.Entity{Description: "smelter"})
	req, _ := http.NewRequest("PATCH", srv.URL+"/v1/entities/e1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)
}
