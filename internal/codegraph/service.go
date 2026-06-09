package codegraph

import (
	"context"
	"fmt"
)

const (
	defaultSearchLimit    = 50
	maxSearchLimit        = 500
	defaultImportantLimit = 20
	maxImportantLimit     = 200
	defaultAmbiguousLimit = 50
	maxAmbiguousLimit     = 500
)

// Service validates requests, applies traversal caps, and delegates to a Store.
type Service struct {
	store   Store
	metrics *Metrics
}

// NewService returns a Service over the given store and metrics.
func NewService(store Store, metrics *Metrics) *Service {
	return &Service{store: store, metrics: metrics}
}

// Push validates that every entity and edge in p is owned by a file in p.Files,
// then reconciles the graph for that file set.
func (s *Service) Push(ctx context.Context, p GraphPush) (PushResult, error) {
	if p.Repo == "" {
		return PushResult{}, fmt.Errorf("%w: repo required", ErrInvalidScope)
	}
	if len(p.Files) == 0 {
		return PushResult{}, fmt.Errorf("%w: files required", ErrInvalidScope)
	}
	files := make(map[string]struct{}, len(p.Files))
	for _, f := range p.Files {
		files[f] = struct{}{}
	}
	for _, e := range p.Entities {
		if e.FilePath == "" {
			continue // repo/package-scoped entity (e.g. go_package): no single owning file
		}
		if _, ok := files[e.FilePath]; !ok {
			return PushResult{}, fmt.Errorf("%w: entity %s file_path %q not in files", ErrInvalidScope, e.ID, e.FilePath)
		}
	}
	for _, e := range p.Edges {
		if _, ok := files[e.SrcFile]; !ok {
			return PushResult{}, fmt.Errorf("%w: edge %s->%s src_file %q not in files", ErrInvalidScope, e.From, e.To, e.SrcFile)
		}
	}
	for _, sym := range p.Symbols {
		if _, ok := files[sym.SrcFile]; !ok {
			return PushResult{}, fmt.Errorf("%w: symbol %s src_file %q not in files", ErrInvalidScope, sym.Symbol, sym.SrcFile)
		}
		if sym.Role != RoleProvides && sym.Role != RoleRequires {
			return PushResult{}, fmt.Errorf("%w: symbol %s role %q must be provides|requires", ErrInvalidScope, sym.Symbol, sym.Role)
		}
	}
	res, err := s.store.Reconcile(ctx, p)
	if err != nil {
		return PushResult{}, err
	}
	s.metrics.observePush(p.Repo, res.EntitiesUpserted, res.EdgesUpserted)
	return res, nil
}

// Search returns entities matching q and typ in repo, with a capped limit.
func (s *Service) Search(ctx context.Context, repo, q, typ string, limit int) ([]Entity, error) {
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	return s.store.SearchEntities(ctx, repo, q, typ, limit)
}

// Entity returns one entity with its immediate edges.
func (s *Service) Entity(ctx context.Context, repo, id string) (EntityDetail, error) {
	return s.store.GetEntity(ctx, repo, id)
}

// Neighbors walks the given relations from id with capped depth and normalized direction.
func (s *Service) Neighbors(ctx context.Context, repo, id string, relations []string, dir string, depth int, cf ConfidenceFilter) ([]PathNode, error) {
	return s.store.Neighbors(ctx, repo, id, relations, normalizeDir(dir), clampDepth(depth), cf)
}

// Callers returns entities that call id (reverse "calls").
func (s *Service) Callers(ctx context.Context, repo, id string, depth int, cf ConfidenceFilter) ([]PathNode, error) {
	return s.Neighbors(ctx, repo, id, callRelations, "in", depth, cf)
}

// Callees returns entities that id calls (forward "calls").
func (s *Service) Callees(ctx context.Context, repo, id string, depth int, cf ConfidenceFilter) ([]PathNode, error) {
	return s.Neighbors(ctx, repo, id, callRelations, "out", depth, cf)
}

// Dependents returns entities that depend on id (reverse imports/references/depends_on).
func (s *Service) Dependents(ctx context.Context, repo, id string, depth int, cf ConfidenceFilter) ([]PathNode, error) {
	return s.Neighbors(ctx, repo, id, dependencyRelations, "in", depth, cf)
}

// Dependencies returns entities that id depends on (forward imports/references/depends_on).
func (s *Service) Dependencies(ctx context.Context, repo, id string, depth int, cf ConfidenceFilter) ([]PathNode, error) {
	return s.Neighbors(ctx, repo, id, dependencyRelations, "out", depth, cf)
}

// ResourceGraph returns the forward infra-dependency subgraph from id (tf/helm relations).
func (s *Service) ResourceGraph(ctx context.Context, repo, id string, depth int, cf ConfidenceFilter) ([]PathNode, error) {
	return s.Neighbors(ctx, repo, id, resourceRelations, "out", depth, cf)
}

// FileImports returns the import edges originating in path.
func (s *Service) FileImports(ctx context.Context, repo, path string) ([]Edge, error) {
	return s.store.FileImports(ctx, repo, path)
}

// CrossRepo returns the cross-repo consumers and providers for an entity.
func (s *Service) CrossRepo(ctx context.Context, repo, id string) (CrossRepoLinks, error) {
	return s.store.CrossRepo(ctx, repo, id)
}

// ShortestPath returns the ordered entity chain from fromID to toID, or empty if unreachable.
func (s *Service) ShortestPath(ctx context.Context, repo, fromID, toID string, relations []string, depth int) ([]Entity, error) {
	return s.store.ShortestPath(ctx, repo, fromID, toID, relations, clampDepth(depth))
}

// ImportantEntities returns entities ranked by degree DESC.
func (s *Service) ImportantEntities(ctx context.Context, repo string, limit int) ([]EntityDegree, error) {
	if limit <= 0 {
		limit = defaultImportantLimit
	}
	if limit > maxImportantLimit {
		limit = maxImportantLimit
	}
	return s.store.ImportantEntities(ctx, repo, limit)
}

// Stats returns aggregate counts for a repo's code graph.
func (s *Service) Stats(ctx context.Context, repo string) (GraphStats, error) {
	return s.store.Stats(ctx, repo)
}

// AmbiguousEdges returns edges with low confidence.
func (s *Service) AmbiguousEdges(ctx context.Context, repo string, limit int) ([]Edge, error) {
	if limit <= 0 {
		limit = defaultAmbiguousLimit
	}
	if limit > maxAmbiguousLimit {
		limit = maxAmbiguousLimit
	}
	return s.store.AmbiguousEdges(ctx, repo, limit)
}

// EntityExplain returns EntityDetail plus labeled neighbor entities.
func (s *Service) EntityExplain(ctx context.Context, repo, id string) (EntityExplain, error) {
	return s.store.EntityExplain(ctx, repo, id)
}
