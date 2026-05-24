package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/auth/testjwks"
	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestProtectedRouteRejectsMissingToken(t *testing.T) {
	tj := testjwks.Start(t)
	defer tj.Close()

	r := httpapi.NewRouter(httpapi.Config{
		Service: &stubService{},
		Verify:  tj.Middleware("tatara-memory"),
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/memories/m1")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestProtectedRouteAcceptsValidToken(t *testing.T) {
	tj := testjwks.Start(t)
	defer tj.Close()

	r := httpapi.NewRouter(httpapi.Config{
		Service: &stubService{getMem: memory.Memory{ID: "m1", Text: "hi"}},
		Verify:  tj.Middleware("tatara-memory"),
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	tok := tj.SignToken(map[string]interface{}{"sub": "u1", "aud": "tatara-memory"})
	req, _ := http.NewRequest("GET", srv.URL+"/v1/memories/m1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}
