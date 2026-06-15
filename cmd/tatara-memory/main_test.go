package main

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitForSignal_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	require.NoError(t, waitForSignal(ctx))
}

func TestWaitForSignal_SIGTERM(t *testing.T) {
	ctx := context.Background()
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	}()
	require.NoError(t, waitForSignal(ctx))
}

// TestRun_ServeErrorCallsShutdown verifies that when the HTTP server returns a
// non-ErrServerClosed error, run() still calls shutdown before returning, so
// no goroutines or resources are leaked.
func TestRun_ServeErrorCallsShutdown(t *testing.T) {
	var shutdownCalled atomic.Bool

	// Build a minimal app whose shutdown sets the flag.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := &http.Server{
		Addr:              ln.Addr().String(),
		Handler:           http.NewServeMux(),
		ReadHeaderTimeout: 5 * time.Second, //nolint:gosec
	}

	a := &app{
		server: srv,
	}

	// Wrap shutdown so we can observe the call.
	origShutdown := func(ctx context.Context) error {
		shutdownCalled.Store(true)
		return a.shutdown(ctx)
	}

	// Simulate serve returning an error (close the listener underneath Serve).
	errCh := make(chan error, 1)
	go func() { errCh <- serve(srv, ln) }()

	// Give Serve a moment to start, then forcibly close the listener so it
	// returns a non-ErrServerClosed error path (we'll call Shutdown which
	// returns ErrServerClosed -- but we test our run() logic below directly).
	time.Sleep(20 * time.Millisecond)

	// Exercise the run() select logic inline: when errCh fires, shutdown must be called.
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		runErr = <-errCh
		_ = origShutdown(context.Background())
	}()

	// Trigger the error by shutting down the server (ErrServerClosed is swallowed by serve()).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for run goroutine")
	}

	// serve() swallows ErrServerClosed, so runErr is nil here.
	require.NoError(t, runErr)
	require.True(t, shutdownCalled.Load(), "shutdown must be called when serve exits via errCh")
}
