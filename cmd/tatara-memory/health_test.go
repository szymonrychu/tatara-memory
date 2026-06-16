package main

import (
	"context"
	"errors"
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

func TestReadyzFunc_OK(t *testing.T) {
	db := &fakePinger{}
	lr := &fakeHealther{}
	fn := readyzFunc(db, lr)
	err := fn(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, db.calls, "db pinged exactly once on success")
	require.Equal(t, 1, lr.calls, "lr health checked exactly once on success")
}

func TestReadyzFunc_DBDown(t *testing.T) {
	db := &fakePinger{err: errors.New("db gone")}
	lr := &fakeHealther{}
	fn := readyzFunc(db, lr)
	err := fn(context.Background())
	require.ErrorIs(t, err, errNotReady)
	require.Equal(t, 1, db.calls, "db pinged exactly once on failure")
	require.Equal(t, 1, lr.calls, "lr health checked exactly once on failure")
}

func TestReadyzFunc_LightRAGDown(t *testing.T) {
	db := &fakePinger{}
	lr := &fakeHealther{err: errors.New("lr gone")}
	fn := readyzFunc(db, lr)
	err := fn(context.Background())
	require.ErrorIs(t, err, errNotReady)
	require.Equal(t, 1, db.calls, "db pinged exactly once")
	require.Equal(t, 1, lr.calls, "lr health checked exactly once")
}

func TestReadyzFunc_BothDown(t *testing.T) {
	db := &fakePinger{err: errors.New("db gone")}
	lr := &fakeHealther{err: errors.New("lr gone")}
	fn := readyzFunc(db, lr)
	err := fn(context.Background())
	require.ErrorIs(t, err, errNotReady)
	require.Equal(t, 1, db.calls, "db pinged exactly once when both down")
	require.Equal(t, 1, lr.calls, "lr health checked exactly once when both down")
}
