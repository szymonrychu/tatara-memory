//go:build integration

package codegraph_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

func TestAnalytics_ComputeAndPersist(t *testing.T) {
	s, db, ctx := freshStoreWithDB(t)

	// Two triangles bridged by c-d.
	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo:  "an",
		Files: []string{"f.go"},
		Entities: []codegraph.Entity{
			ent("an:a", "go_func", "f.go"), ent("an:b", "go_func", "f.go"), ent("an:c", "go_func", "f.go"),
			ent("an:d", "go_func", "f.go"), ent("an:e", "go_func", "f.go"), ent("an:f", "go_func", "f.go"),
		},
		Edges: []codegraph.Edge{
			{From: "an:a", To: "an:b", Relation: "calls", SrcFile: "f.go"},
			{From: "an:b", To: "an:c", Relation: "calls", SrcFile: "f.go"},
			{From: "an:a", To: "an:c", Relation: "calls", SrcFile: "f.go"},
			{From: "an:d", To: "an:e", Relation: "calls", SrcFile: "f.go"},
			{From: "an:e", To: "an:f", Relation: "calls", SrcFile: "f.go"},
			{From: "an:d", To: "an:f", Relation: "calls", SrcFile: "f.go"},
			{From: "an:c", To: "an:d", Relation: "calls", SrcFile: "f.go"},
		},
	})
	require.NoError(t, err)

	// nil labeler -> label = top-degree member name.
	res, err := s.RecomputeAnalytics(ctx, "an", nil, 0)
	require.NoError(t, err)
	require.Equal(t, 2, res.Communities)
	require.Greater(t, res.Entities, 0)

	// Entity columns persisted. degree column was dropped (finding 4 - dead write-only column).
	var community int
	var betweenness float64
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT community, betweenness FROM code_entities WHERE repo='an' AND id='an:c'`).
		Scan(&community, &betweenness))
	require.Greater(t, betweenness, 0.0)

	// Two communities persisted in code_communities with non-empty labels.
	var nComm int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM code_communities WHERE repo='an'`).Scan(&nComm))
	require.Equal(t, 2, nComm)
	var label string
	var size int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT label, size FROM code_communities WHERE repo='an' ORDER BY community LIMIT 1`).Scan(&label, &size))
	require.NotEmpty(t, label)
	require.Equal(t, 3, size)

	// State cleared: dirty=false, computed_at set.
	var dirty bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT dirty FROM repo_analytics_state WHERE repo='an'`).Scan(&dirty))
	require.False(t, dirty)
}

func TestAnalytics_DirtyReposListing(t *testing.T) {
	s, db, ctx := freshStoreWithDB(t)
	_, err := s.Reconcile(ctx, codegraph.GraphPush{
		Repo: "dr", Files: []string{"a.go"}, Entities: []codegraph.Entity{ent("dr:a", "go_func", "a.go")},
	})
	require.NoError(t, err)
	// Backdate reconciled_at so debounce passes immediately.
	_, err = db.ExecContext(ctx, `UPDATE repo_analytics_state SET reconciled_at = now() - interval '5 minutes' WHERE repo='dr'`)
	require.NoError(t, err)

	repos, err := s.DirtyRepos(ctx, 60) // debounce 60s
	require.NoError(t, err)
	require.Contains(t, repos, "dr")

	_, err = s.RecomputeAnalytics(ctx, "dr", nil, 0)
	require.NoError(t, err)
	repos2, err := s.DirtyRepos(ctx, 60)
	require.NoError(t, err)
	require.NotContains(t, repos2, "dr")
}
