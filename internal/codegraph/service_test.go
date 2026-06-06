package codegraph_test

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

type fakeStore struct {
	pushed  codegraph.GraphPush
	lastRel []string
	lastDir string
	lastDep int
}

func (f *fakeStore) Reconcile(_ context.Context, p codegraph.GraphPush) (codegraph.PushResult, error) {
	f.pushed = p
	return codegraph.PushResult{Repo: p.Repo, Files: len(p.Files), EntitiesUpserted: len(p.Entities), EdgesUpserted: len(p.Edges)}, nil
}
func (f *fakeStore) SearchEntities(_ context.Context, _, _, _ string, _ int) ([]codegraph.Entity, error) {
	return nil, nil
}
func (f *fakeStore) GetEntity(_ context.Context, _, _ string) (codegraph.EntityDetail, error) {
	return codegraph.EntityDetail{}, nil
}
func (f *fakeStore) Neighbors(_ context.Context, _, _ string, relations []string, dir string, depth int) ([]codegraph.PathNode, error) {
	f.lastRel, f.lastDir, f.lastDep = relations, dir, depth
	return nil, nil
}
func (f *fakeStore) FileImports(_ context.Context, _, _ string) ([]codegraph.Edge, error) {
	return nil, nil
}
func (f *fakeStore) CountEntities(_ context.Context, _ string) (int, error) { return 0, nil }

func newSvc() (*codegraph.Service, *fakeStore) {
	fs := &fakeStore{}
	return codegraph.NewService(fs, codegraph.NewMetrics(prometheus.NewRegistry())), fs
}

func TestPushRejectsEntityOutsideFiles(t *testing.T) {
	svc, _ := newSvc()
	_, err := svc.Push(context.Background(), codegraph.GraphPush{
		Repo:     "r",
		Files:    []string{"a.go"},
		Entities: []codegraph.Entity{{ID: "x", FilePath: "b.go"}},
	})
	require.ErrorIs(t, err, codegraph.ErrInvalidScope)
}

func TestPushRejectsEdgeOutsideFiles(t *testing.T) {
	svc, _ := newSvc()
	_, err := svc.Push(context.Background(), codegraph.GraphPush{
		Repo:  "r",
		Files: []string{"a.go"},
		Edges: []codegraph.Edge{{From: "x", To: "y", Relation: "calls", SrcFile: "b.go"}},
	})
	require.ErrorIs(t, err, codegraph.ErrInvalidScope)
}

func TestPushRequiresRepoAndFiles(t *testing.T) {
	svc, _ := newSvc()
	_, err := svc.Push(context.Background(), codegraph.GraphPush{Repo: "", Files: []string{"a.go"}})
	require.ErrorIs(t, err, codegraph.ErrInvalidScope)
	_, err = svc.Push(context.Background(), codegraph.GraphPush{Repo: "r", Files: nil})
	require.ErrorIs(t, err, codegraph.ErrInvalidScope)
}

func TestPushOK(t *testing.T) {
	svc, fs := newSvc()
	res, err := svc.Push(context.Background(), codegraph.GraphPush{
		Repo:     "r",
		Files:    []string{"a.go"},
		Entities: []codegraph.Entity{{ID: "x", FilePath: "a.go"}},
		Edges:    []codegraph.Edge{{From: "x", To: "y", Relation: "calls", SrcFile: "a.go"}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, res.EntitiesUpserted)
	require.Equal(t, "r", fs.pushed.Repo)
}

func TestNamedTraversalsUseCorrectRelationSets(t *testing.T) {
	svc, fs := newSvc()
	ctx := context.Background()

	_, _ = svc.Callers(ctx, "r", "id", 0)
	require.Equal(t, []string{"calls"}, fs.lastRel)
	require.Equal(t, "in", fs.lastDir)
	require.Equal(t, 3, fs.lastDep)

	_, _ = svc.Callees(ctx, "r", "id", 50)
	require.Equal(t, "out", fs.lastDir)
	require.Equal(t, 10, fs.lastDep)

	_, _ = svc.Dependents(ctx, "r", "id", 2)
	require.Equal(t, []string{"imports", "references", "depends_on"}, fs.lastRel)
	require.Equal(t, "in", fs.lastDir)

	_, _ = svc.ResourceGraph(ctx, "r", "id", 1)
	require.Equal(t, []string{"depends_on", "references", "value_ref", "includes", "subchart", "module_source"}, fs.lastRel)
	require.Equal(t, "out", fs.lastDir)
}
