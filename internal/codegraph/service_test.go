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
	lastLim int
	lastCF  codegraph.ConfidenceFilter
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
func (f *fakeStore) Neighbors(_ context.Context, _, _ string, relations []string, dir string, depth, limit int, cf codegraph.ConfidenceFilter) ([]codegraph.PathNode, error) {
	f.lastRel, f.lastDir, f.lastDep, f.lastLim, f.lastCF = relations, dir, depth, limit, cf
	return nil, nil
}
func (f *fakeStore) FileImports(_ context.Context, _, _ string) ([]codegraph.Edge, error) {
	return nil, nil
}
func (f *fakeStore) CountEntities(_ context.Context, _ string) (int, error) { return 0, nil }
func (f *fakeStore) CrossRepo(_ context.Context, _, _ string) (codegraph.CrossRepoLinks, error) {
	return codegraph.CrossRepoLinks{Consumers: []codegraph.CrossRef{}, Providers: []codegraph.CrossRef{}}, nil
}
func (f *fakeStore) ShortestPath(_ context.Context, _, _, _ string, _ []string, _ int) ([]codegraph.Entity, error) {
	return nil, nil
}
func (f *fakeStore) ImportantEntities(_ context.Context, _ string, _ int) ([]codegraph.EntityDegree, error) {
	return nil, nil
}
func (f *fakeStore) Stats(_ context.Context, _ string) (codegraph.GraphStats, error) {
	return codegraph.GraphStats{EntitiesByType: map[string]int{}, EdgesByRelation: map[string]int{}, EdgesByTier: map[string]int{}}, nil
}
func (f *fakeStore) AmbiguousEdges(_ context.Context, _ string, _ int) ([]codegraph.Edge, error) {
	return nil, nil
}
func (f *fakeStore) EntityExplain(_ context.Context, _, _ string) (codegraph.EntityExplain, error) {
	return codegraph.EntityExplain{}, nil
}
func (f *fakeStore) SemanticMisses(_ context.Context, _ string, _ []codegraph.FileSHA) ([]string, error) {
	return nil, nil
}
func (f *fakeStore) Related(_ context.Context, _, _ string, _ []string, _ float64) ([]codegraph.RelatedResult, error) {
	return nil, nil
}
func (f *fakeStore) Hyperedges(_ context.Context, _, _ string) ([]codegraph.Hyperedge, error) {
	return nil, nil
}
func (f *fakeStore) Hyperedge(_ context.Context, _, _ string) (codegraph.Hyperedge, error) {
	return codegraph.Hyperedge{}, nil
}
func (f *fakeStore) Communities(_ context.Context, _ string) ([]codegraph.CommunityRow, error) {
	return nil, nil
}
func (f *fakeStore) Community(_ context.Context, _ string, _ int) ([]codegraph.Entity, error) {
	return nil, nil
}
func (f *fakeStore) Bridges(_ context.Context, _ string, _ int) ([]codegraph.Bridge, error) {
	return nil, nil
}
func (f *fakeStore) ImportantEntitiesBy(_ context.Context, _, _ string, _ int) ([]codegraph.EntityDegree, error) {
	return nil, nil
}
func (f *fakeStore) DirtyRepos(_ context.Context, _ int) ([]string, error) { return nil, nil }
func (f *fakeStore) RecomputeAnalytics(_ context.Context, _ string, _ codegraph.CommunityLabeler) (codegraph.RecomputeResult, error) {
	return codegraph.RecomputeResult{}, nil
}

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

func TestPushAllowsFilelessEntity(t *testing.T) {
	svc, fs := newSvc()
	_, err := svc.Push(context.Background(), codegraph.GraphPush{
		Repo:  "r",
		Files: []string{"a.go"},
		Entities: []codegraph.Entity{
			{ID: "go:func:a", FilePath: "a.go"},
			{ID: "go:package:p", FilePath: ""}, // repo/package-scoped: no single owning file
		},
	})
	require.NoError(t, err)
	require.Len(t, fs.pushed.Entities, 2)
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

func TestPushRejectsSymbolOutsideFiles(t *testing.T) {
	svc, _ := newSvc()
	_, err := svc.Push(context.Background(), codegraph.GraphPush{
		Repo:  "r",
		Files: []string{"a.go"},
		Symbols: []codegraph.SymbolRow{
			{Symbol: "Foo", Lang: "go", Kind: "func", Role: codegraph.RoleProvides, EntityID: "e1", SrcFile: "b.go"},
		},
	})
	require.ErrorIs(t, err, codegraph.ErrInvalidScope)
}

func TestPushRejectsSymbolInvalidRole(t *testing.T) {
	svc, _ := newSvc()
	_, err := svc.Push(context.Background(), codegraph.GraphPush{
		Repo:  "r",
		Files: []string{"a.go"},
		Symbols: []codegraph.SymbolRow{
			{Symbol: "Foo", Lang: "go", Kind: "func", Role: "bad_role", EntityID: "e1", SrcFile: "a.go"},
		},
	})
	require.ErrorIs(t, err, codegraph.ErrInvalidScope)
}

func TestPushBackCompatNoSymbols(t *testing.T) {
	svc, _ := newSvc()
	_, err := svc.Push(context.Background(), codegraph.GraphPush{
		Repo:     "r",
		Files:    []string{"a.go"},
		Entities: []codegraph.Entity{{ID: "x", FilePath: "a.go"}},
	})
	require.NoError(t, err)
}

func TestPushWithValidSymbols(t *testing.T) {
	svc, _ := newSvc()
	_, err := svc.Push(context.Background(), codegraph.GraphPush{
		Repo:  "r",
		Files: []string{"a.go"},
		Symbols: []codegraph.SymbolRow{
			{Symbol: "Foo", Lang: "go", Kind: "func", Role: codegraph.RoleProvides, EntityID: "e1", SrcFile: "a.go"},
			{Symbol: "Bar", Lang: "go", Kind: "func", Role: codegraph.RoleRequires, EntityID: "e2", SrcFile: "a.go"},
		},
	})
	require.NoError(t, err)
}

func TestCrossRepoPassThrough(t *testing.T) {
	svc, _ := newSvc()
	links, err := svc.CrossRepo(context.Background(), "r", "e1")
	require.NoError(t, err)
	// fakeStore returns zero value
	require.NotNil(t, links.Consumers)
}

func TestNamedTraversalsUseCorrectRelationSets(t *testing.T) {
	svc, fs := newSvc()
	ctx := context.Background()
	noCF := codegraph.ConfidenceFilter{}

	_, _ = svc.Callers(ctx, "r", "id", 0, noCF)
	require.Equal(t, []string{"calls"}, fs.lastRel)
	require.Equal(t, "in", fs.lastDir)
	require.Equal(t, 3, fs.lastDep)
	require.Equal(t, 1000, fs.lastLim) // named traversals get the default breadth cap

	_, _ = svc.Callees(ctx, "r", "id", 50, noCF)
	require.Equal(t, "out", fs.lastDir)
	require.Equal(t, 10, fs.lastDep)

	_, _ = svc.Dependents(ctx, "r", "id", 2, noCF)
	require.Equal(t, []string{"imports", "references", "depends_on"}, fs.lastRel)
	require.Equal(t, "in", fs.lastDir)

	_, _ = svc.ResourceGraph(ctx, "r", "id", 1, noCF)
	require.Equal(t, []string{"depends_on", "references", "value_ref", "includes", "subchart", "module_source"}, fs.lastRel)
	require.Equal(t, "out", fs.lastDir)
}

func TestNeighborsClampsBreadthLimit(t *testing.T) {
	svc, fs := newSvc()
	ctx := context.Background()
	noCF := codegraph.ConfidenceFilter{}

	_, _ = svc.Neighbors(ctx, "r", "id", []string{"calls"}, "out", 0, 0, noCF)
	require.Equal(t, 1000, fs.lastLim) // zero -> default

	_, _ = svc.Neighbors(ctx, "r", "id", []string{"calls"}, "out", 0, 999999, noCF)
	require.Equal(t, 5000, fs.lastLim) // over max -> capped

	_, _ = svc.Neighbors(ctx, "r", "id", []string{"calls"}, "out", 0, 42, noCF)
	require.Equal(t, 42, fs.lastLim) // in range -> passed through
}

func TestConfidenceFilterPassedThrough(t *testing.T) {
	svc, fs := newSvc()
	ctx := context.Background()
	cf := codegraph.ConfidenceFilter{MinConfidence: 0.8, Tier: codegraph.TierInferred}

	_, _ = svc.Callers(ctx, "r", "id", 0, cf)
	require.Equal(t, cf, fs.lastCF)
}

func TestNewServiceMethods(t *testing.T) {
	svc, _ := newSvc()
	ctx := context.Background()

	chain, err := svc.ShortestPath(ctx, "r", "a", "b", nil, 5)
	require.NoError(t, err)
	require.Nil(t, chain)

	degs, err := svc.ImportantEntities(ctx, "r", 10)
	require.NoError(t, err)
	require.Nil(t, degs)

	stats, err := svc.Stats(ctx, "r")
	require.NoError(t, err)
	require.NotNil(t, stats.EntitiesByType)

	edges, err := svc.AmbiguousEdges(ctx, "r", 10)
	require.NoError(t, err)
	require.Nil(t, edges)

	ex, err := svc.EntityExplain(ctx, "r", "id")
	require.NoError(t, err)
	require.Equal(t, "", ex.ID)
}
