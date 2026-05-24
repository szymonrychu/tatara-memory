package main

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServe_GracefulShutdown(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: mux, ReadHeaderTimeout: 5 * time.Second} //nolint:gosec
	ln, err := newListener(srv.Addr)
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() { errCh <- serve(srv, ln) }()

	time.Sleep(50 * time.Millisecond)
	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz") //nolint:noctx
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, srv.Shutdown(ctx))
	require.NoError(t, <-errCh)
}
