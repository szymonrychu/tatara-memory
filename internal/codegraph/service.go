package codegraph

import (
	"context"
	"fmt"
)

const (
	defaultSearchLimit = 50
	maxSearchLimit     = 500
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
		if _, ok := files[e.FilePath]; !ok {
			return PushResult{}, fmt.Errorf("%w: entity %s file_path %q not in files", ErrInvalidScope, e.ID, e.FilePath)
		}
	}
	for _, e := range p.Edges {
		if _, ok := files[e.SrcFile]; !ok {
			return PushResult{}, fmt.Errorf("%w: edge %s->%s src_file %q not in files", ErrInvalidScope, e.From, e.To, e.SrcFile)
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
func (s *Service) Neighbors(ctx context.Context, repo, id string, relations []string, dir string, depth int) ([]PathNode, error) {
	return s.store.Neighbors(ctx, repo, id, relations, normalizeDir(dir), clampDepth(depth))
}

// Callers returns entities that call id (reverse "calls").
func (s *Service) Callers(ctx context.Context, repo, id string, depth int) ([]PathNode, error) {
	return s.Neighbors(ctx, repo, id, callRelations, "in", depth)
}

// Callees returns entities that id calls (forward "calls").
func (s *Service) Callees(ctx context.Context, repo, id string, depth int) ([]PathNode, error) {
	return s.Neighbors(ctx, repo, id, callRelations, "out", depth)
}

// Dependents returns entities that depend on id (reverse imports/references/depends_on).
func (s *Service) Dependents(ctx context.Context, repo, id string, depth int) ([]PathNode, error) {
	return s.Neighbors(ctx, repo, id, dependencyRelations, "in", depth)
}

// Dependencies returns entities that id depends on (forward imports/references/depends_on).
func (s *Service) Dependencies(ctx context.Context, repo, id string, depth int) ([]PathNode, error) {
	return s.Neighbors(ctx, repo, id, dependencyRelations, "out", depth)
}

// ResourceGraph returns the forward infra-dependency subgraph from id (tf/helm relations).
func (s *Service) ResourceGraph(ctx context.Context, repo, id string, depth int) ([]PathNode, error) {
	return s.Neighbors(ctx, repo, id, resourceRelations, "out", depth)
}

// FileImports returns the import edges originating in path.
func (s *Service) FileImports(ctx context.Context, repo, path string) ([]Edge, error) {
	return s.store.FileImports(ctx, repo, path)
}
