package httpapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func newSrv(t *testing.T, svc httpapi.MemoryService) *httptest.Server {
	t.Helper()
	return httptest.NewServer(httpapi.NewRouter(httpapi.Config{Service: svc}))
}

func TestPostMemory201(t *testing.T) {
	srv := newSrv(t, &stubService{})
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"text": "hello"})
	resp, err := http.Post(srv.URL+"/memories", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var m memory.Memory
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&m))
	require.NotEmpty(t, m.ID)
}

func TestPostMemoryBadJSON400(t *testing.T) {
	srv := newSrv(t, &stubService{})
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/memories", "application/json", bytes.NewReader([]byte("not-json")))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestGetMemoryNotFound(t *testing.T) {
	srv := newSrv(t, &stubService{getErr: memory.ErrNotFound})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/memories/missing")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestGetMemoryUpstream502(t *testing.T) {
	srv := newSrv(t, &stubService{getErr: errors.Join(memory.ErrUpstream, errors.New("lr down"))})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/memories/x")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

func TestGetMemoryTransient503(t *testing.T) {
	srv := newSrv(t, &stubService{getErr: errors.Join(memory.ErrTransient, errors.New("timeout"))})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/memories/x")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	require.NotEmpty(t, resp.Header.Get("Retry-After"))
}

func TestDeleteMemory204(t *testing.T) {
	srv := newSrv(t, &stubService{})
	defer srv.Close()

	req, _ := http.NewRequest("DELETE", srv.URL+"/memories/m1", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}
