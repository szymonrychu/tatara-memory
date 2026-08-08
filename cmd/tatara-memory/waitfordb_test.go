package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These pin the retry primitive behind tatara-memory#102's fix. On origin/main
// waitForDB had a hardcoded caller (60s budget, fixed 2s interval) whose expiry
// propagated to main's os.Exit(1), so a ~4-minute Postgres outage became a
// ~8-minute CrashLoopBackOff outage.

// TestWaitForDB_NonPositiveTimeoutRetriesUntilShutdown pins the semantics that
// make the crash loop impossible: a non-positive budget means "keep polling for
// as long as this process is alive", not "give up on the first failure". On
// origin/main a zero timeout put the deadline in the past, so the very first
// failed ping returned "database not reachable within 0s".
func TestWaitForDB_NonPositiveTimeoutRetriesUntilShutdown(t *testing.T) {
	var n atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	probe := func(context.Context) error {
		if n.Add(1) >= 5 {
			cancel()
		}
		return errors.New("connection refused")
	}

	err := waitForDB(ctx, probe, 0, time.Millisecond)
	require.ErrorIs(t, err, context.Canceled,
		"an unlimited wait must only end when the process is shutting down (tatara-memory#102)")
	require.GreaterOrEqual(t, int(n.Load()), 5,
		"the wait gave up after %d attempt(s) instead of retrying", n.Load())
}

// TestWaitForDB_BacksOffBetweenAttempts: the retry interval must grow. A fixed
// 2s poll against a dependency that is down for minutes is pure noise on both
// sides; backing off is what makes an unlimited wait cheap enough to be the
// default.
func TestWaitForDB_BacksOffBetweenAttempts(t *testing.T) {
	var mu sync.Mutex
	var at []time.Time

	probe := func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		at = append(at, time.Now())
		if len(at) >= 4 {
			return nil
		}
		return errors.New("connection refused")
	}

	require.NoError(t, waitForDB(context.Background(), probe, time.Minute, 20*time.Millisecond))
	require.Len(t, at, 4)

	first, second, third := at[1].Sub(at[0]), at[2].Sub(at[1]), at[3].Sub(at[2])
	require.Greater(t, second, first+5*time.Millisecond,
		"interval did not grow: %s then %s", first, second)
	require.Greater(t, third, second+5*time.Millisecond,
		"interval did not grow: %s then %s", second, third)
}

// TestWaitForDB_ExpiredBudgetReportsTheUnderlyingError keeps the diagnosis the
// old message had ("database not reachable within 1m0s") and adds the cause,
// which the old one dropped entirely.
func TestWaitForDB_ExpiredBudgetReportsTheUnderlyingError(t *testing.T) {
	cause := errors.New("connection refused")
	err := waitForDB(context.Background(), func(context.Context) error { return cause },
		20*time.Millisecond, 5*time.Millisecond)
	require.Error(t, err)
	require.ErrorIs(t, err, cause, "the expiry error must carry why the ping failed")
	require.Contains(t, err.Error(), "20ms")
}
