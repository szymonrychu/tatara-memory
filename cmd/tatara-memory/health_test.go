package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakePinger struct{ err error }

func (f fakePinger) PingContext(_ context.Context) error { return f.err }

type fakeHealther struct{ err error }

func (f fakeHealther) Health(_ context.Context) error { return f.err }

func TestHealthz(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthzHandler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "ok", rr.Body.String())
}

func TestReadyz_OK(t *testing.T) {
	h := readyzHandler(fakePinger{}, fakeHealther{})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestReadyz_DBDown(t *testing.T) {
	h := readyzHandler(fakePinger{err: errors.New("db gone")}, fakeHealther{})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
	require.Contains(t, rr.Body.String(), "db")
}

func TestReadyz_LightRAGDown(t *testing.T) {
	h := readyzHandler(fakePinger{}, fakeHealther{err: errors.New("lr gone")})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
	require.Contains(t, rr.Body.String(), "lightrag")
}
