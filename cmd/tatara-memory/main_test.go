package main

import (
	"context"
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
