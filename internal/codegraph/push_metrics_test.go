package codegraph_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

// pushStore implements only Reconcile; every other Store method is the embedded
// nil interface and would panic if called. Push touches nothing else.
type pushStore struct {
	codegraph.Store
	err error
}

func (p *pushStore) Reconcile(_ context.Context, g codegraph.GraphPush) (codegraph.PushResult, error) {
	if p.err != nil {
		return codegraph.PushResult{}, p.err
	}
	return codegraph.PushResult{Repo: g.Repo, EntitiesUpserted: len(g.Entities)}, nil
}

func pushOnce(t *testing.T, storeErr error) *prometheus.Registry {
	t.Helper()
	reg := prometheus.NewRegistry()
	svc := codegraph.NewService(&pushStore{err: storeErr}, codegraph.NewMetrics(reg))
	_, err := svc.Push(context.Background(), codegraph.GraphPush{
		Repo:     "mtg-decks",
		Files:    []string{"a.py"},
		Entities: []codegraph.Entity{{ID: "e1", FilePath: "a.py"}},
	})
	if storeErr == nil {
		require.NoError(t, err)
	} else {
		require.Error(t, err)
	}
	return reg
}

// TestPushTotal_CountsOutcomes is the detection half of tatara-memory#98.
//
// For seven hours the only evidence that code-graph writes were dead was
// code_graph_entities_upserted_total{repo="mtg-decks"} sitting frozen at
// 13668 - a counter that does not move, which is indistinguishable from a repo
// nobody is pushing to. observePush was called on the success path only
// (internal/codegraph/service.go), so a failing push produced no signal at all
// and there was nothing an alert could be written against. This counter makes
// failure a positive, labelled observation, which is what CLAUDE.md rule 13
// ("metrics for everything that counts, times out, or can fail") asks for and
// what the accompanying PrometheusRule alerts on.
func TestPushTotal_CountsOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		result string
	}{
		{"success", nil, "success"},
		{"lock timeout", fmt.Errorf("x: %w", codegraph.ErrLockTimeout), "lock_timeout"},
		{"deadlock", fmt.Errorf("x: %w", codegraph.ErrDeadlock), "deadlock"},
		{"budget exhausted", fmt.Errorf("x: %w", codegraph.ErrReconcileTimeout), "timeout"},
		{"anything else", errors.New("relation does not exist"), "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := pushOnce(t, tc.err)
			require.Equal(t, 1.0, pushCount(t, reg, "mtg-decks", tc.result),
				"one push with this outcome must produce exactly one observation on that result label")
		})
	}
}

// TestPushTotal_InvalidScopeIsNotCounted keeps the counter about the write
// path. A push rejected by validation never reaches Postgres, so counting it
// would put client mistakes on the same series an operator alerts on.
func TestPushTotal_InvalidScopeIsNotCounted(t *testing.T) {
	reg := prometheus.NewRegistry()
	svc := codegraph.NewService(&pushStore{}, codegraph.NewMetrics(reg))

	_, err := svc.Push(context.Background(), codegraph.GraphPush{Repo: "", Files: []string{"a.py"}})
	require.ErrorIs(t, err, codegraph.ErrInvalidScope)

	require.Equal(t, 0.0, pushCount(t, reg, "", "error"))
}

// pushCount reads one label pair off the gathered code_graph_push_total
// family. Gathering directly keeps the assertion about the exported series
// rather than about an internal handle.
func pushCount(t *testing.T, reg *prometheus.Registry, repo, result string) float64 {
	t.Helper()
	fams, err := reg.Gather()
	require.NoError(t, err)
	for _, f := range fams {
		if f.GetName() != "code_graph_push_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			var gotRepo, gotResult string
			for _, l := range m.GetLabel() {
				switch l.GetName() {
				case "repo":
					gotRepo = l.GetValue()
				case "result":
					gotResult = l.GetValue()
				}
			}
			if gotRepo == repo && gotResult == result {
				return m.GetCounter().GetValue()
			}
		}
		return 0
	}
	// An absent family means no push has been observed at all, which is zero
	// for every label pair. TestPushTotal_CountsOutcomes is what proves the
	// metric is registered: it asserts 1, which an absent family cannot give.
	return 0
}
