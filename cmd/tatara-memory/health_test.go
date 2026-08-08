package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakePinger struct {
	err   error
	calls int
}

func (f *fakePinger) PingContext(_ context.Context) error {
	f.calls++
	return f.err
}

type fakeHealther struct {
	err   error
	calls int
}

func (f *fakeHealther) Health(_ context.Context) error {
	f.calls++
	return f.err
}

// TestReadyzFunc_StartupGateComesFirst pins the readiness half of
// tatara-memory#102: while the background database wait is still running this
// pod is up but must not take traffic, and it must say so without touching
// Postgres - pinging a database that is known to be down only adds latency to
// every probe.
func TestReadyzFunc_StartupGateComesFirst(t *testing.T) {
	startupErr := errors.New("database not reachable")
	db := &fakePinger{}
	lr := &fakeHealther{}

	fn := readyzFunc(func() error { return startupErr }, db, lr)
	err := fn(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, startupErr, "the startup reason must reach the probe")
	require.True(t, strings.HasPrefix(err.Error(), "startup:"), "error must be prefixed with 'startup:'")
	require.Equal(t, 0, db.calls, "no point pinging a database the startup wait is already retrying")
	require.Equal(t, 0, lr.calls)
}

// TestReadyzFunc_StartupCompleteFallsThrough: once startup has finished the
// gate stops mattering and the live dependency checks take over again.
func TestReadyzFunc_StartupCompleteFallsThrough(t *testing.T) {
	db := &fakePinger{}
	lr := &fakeHealther{}

	fn := readyzFunc(func() error { return nil }, db, lr)
	require.NoError(t, fn(context.Background()))
	require.Equal(t, 1, db.calls)
	require.Equal(t, 1, lr.calls)
}

func TestReadyzFunc_OK(t *testing.T) {
	db := &fakePinger{}
	lr := &fakeHealther{}
	fn := readyzFunc(nil, db, lr)
	err := fn(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, db.calls, "db pinged exactly once on success")
	require.Equal(t, 1, lr.calls, "lr health checked exactly once on success")
}

func TestReadyzFunc_DBDown(t *testing.T) {
	dbErr := errors.New("db gone")
	db := &fakePinger{err: dbErr}
	lr := &fakeHealther{}
	fn := readyzFunc(nil, db, lr)
	err := fn(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, dbErr, "wrapped db error must be unwrappable")
	require.True(t, strings.HasPrefix(err.Error(), "db:"), "error must be prefixed with 'db:'")
	require.Equal(t, 1, db.calls, "db pinged exactly once on failure")
	// lr is not called once db fails (fail-fast)
	require.Equal(t, 0, lr.calls, "lr not checked when db is down")
}

func TestReadyzFunc_LightRAGDown(t *testing.T) {
	lrErr := errors.New("lr gone")
	db := &fakePinger{}
	lr := &fakeHealther{err: lrErr}
	fn := readyzFunc(nil, db, lr)
	err := fn(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, lrErr, "wrapped lightrag error must be unwrappable")
	require.True(t, strings.HasPrefix(err.Error(), "lightrag:"), "error must be prefixed with 'lightrag:'")
	require.Equal(t, 1, db.calls, "db pinged exactly once")
	require.Equal(t, 1, lr.calls, "lr health checked exactly once")
}

func TestReadyzFunc_BothDown(t *testing.T) {
	dbErr := errors.New("db gone")
	db := &fakePinger{err: dbErr}
	lr := &fakeHealther{err: errors.New("lr gone")}
	fn := readyzFunc(nil, db, lr)
	err := fn(context.Background())
	require.Error(t, err)
	// db is checked first; lr is not reached
	require.ErrorIs(t, err, dbErr, "db error must be the one returned when both are down")
	require.Equal(t, 1, db.calls, "db pinged exactly once when both down")
	require.Equal(t, 0, lr.calls, "lr not checked when db is already down")
}
