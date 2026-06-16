package codegraph

import (
	"context"
	"fmt"
	"time"
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
	start := time.Now()
	out, err := s.store.SearchEntities(ctx, repo, q, typ, limit)
	s.metrics.observeQuery(queryOpSearch, start, err)
	return out, err
}

// Entity returns one entity with its immediate edges.
func (s *Service) Entity(ctx context.Context, repo, id string) (EntityDetail, error) {
	start := time.Now()
	out, err := s.store.GetEntity(ctx, repo, id)
	s.metrics.observeQuery(queryOpEntity, start, err)
	return out, err
}

// Neighbors walks the given relations from id with capped depth and breadth and
// normalized direction. limit bounds the number of distinct nodes returned;
// zero applies the default cap.
func (s *Service) Neighbors(ctx context.Context, repo, id string, relations []string, dir string, depth, limit int, cf ConfidenceFilter) ([]PathNode, error) {
	start := time.Now()
	out, err := s.store.Neighbors(ctx, repo, id, relations, normalizeDir(dir), clampDepth(depth), clampNeighborLimit(limit), cf)
	s.metrics.observeQuery(queryOpNeighbors, start, err)
	return out, err
}

// Callers returns entities that call id (reverse "calls").
func (s *Service) Callers(ctx context.Context, repo, id string, depth int, cf ConfidenceFilter) ([]PathNode, error) {
	return s.Neighbors(ctx, repo, id, callRelations, "in", depth, 0, cf)
}

// Callees returns entities that id calls (forward "calls").
func (s *Service) Callees(ctx context.Context, repo, id string, depth int, cf ConfidenceFilter) ([]PathNode, error) {
	return s.Neighbors(ctx, repo, id, callRelations, "out", depth, 0, cf)
}

// Dependents returns entities that depend on id (reverse imports/references/depends_on).
func (s *Service) Dependents(ctx context.Context, repo, id string, depth int, cf ConfidenceFilter) ([]PathNode, error) {
	return s.Neighbors(ctx, repo, id, dependencyRelations, "in", depth, 0, cf)
}

// Dependencies returns entities that id depends on (forward imports/references/depends_on).
func (s *Service) Dependencies(ctx context.Context, repo, id string, depth int, cf ConfidenceFilter) ([]PathNode, error) {
	return s.Neighbors(ctx, repo, id, dependencyRelations, "out", depth, 0, cf)
}

// ResourceGraph returns the forward infra-dependency subgraph from id (tf/helm relations).
func (s *Service) ResourceGraph(ctx context.Context, repo, id string, depth int, cf ConfidenceFilter) ([]PathNode, error) {
	return s.Neighbors(ctx, repo, id, resourceRelations, "out", depth, 0, cf)
}

// FileImports returns the import edges originating in path, capped at limit rows.
func (s *Service) FileImports(ctx context.Context, repo, path string, limit int) ([]Edge, error) {
	start := time.Now()
	out, err := s.store.FileImports(ctx, repo, path, limit)
	s.metrics.observeQuery(queryOpFileImports, start, err)
	return out, err
}

// CrossRepo returns the cross-repo consumers and providers for an entity, capped at limit rows each.
func (s *Service) CrossRepo(ctx context.Context, repo, id string, limit int) (CrossRepoLinks, error) {
	start := time.Now()
	out, err := s.store.CrossRepo(ctx, repo, id, limit)
	s.metrics.observeQuery(queryOpCrossRepo, start, err)
	return out, err
}

// ShortestPath returns the ordered entity chain from fromID to toID, or empty if unreachable.
func (s *Service) ShortestPath(ctx context.Context, repo, fromID, toID string, relations []string, depth int) ([]Entity, error) {
	start := time.Now()
	out, err := s.store.ShortestPath(ctx, repo, fromID, toID, relations, clampDepth(depth))
	s.metrics.observeQuery(queryOpShortestPath, start, err)
	return out, err
}

// ImportantEntities returns entities ranked by degree DESC.
func (s *Service) ImportantEntities(ctx context.Context, repo string, limit int) ([]EntityDegree, error) {
	if limit <= 0 {
		limit = defaultImportantLimit
	}
	if limit > maxImportantLimit {
		limit = maxImportantLimit
	}
	start := time.Now()
	out, err := s.store.ImportantEntities(ctx, repo, limit)
	s.metrics.observeQuery(queryOpImportantEntities, start, err)
	return out, err
}

// Stats returns aggregate counts for a repo's code graph.
func (s *Service) Stats(ctx context.Context, repo string) (GraphStats, error) {
	start := time.Now()
	out, err := s.store.Stats(ctx, repo)
	s.metrics.observeQuery(queryOpStats, start, err)
	return out, err
}

// AmbiguousEdges returns edges with low confidence.
func (s *Service) AmbiguousEdges(ctx context.Context, repo string, limit int) ([]Edge, error) {
	if limit <= 0 {
		limit = defaultAmbiguousLimit
	}
	if limit > maxAmbiguousLimit {
		limit = maxAmbiguousLimit
	}
	start := time.Now()
	out, err := s.store.AmbiguousEdges(ctx, repo, limit)
	s.metrics.observeQuery(queryOpAmbiguous, start, err)
	return out, err
}

// EntityExplain returns EntityDetail plus labeled neighbor entities.
func (s *Service) EntityExplain(ctx context.Context, repo, id string) (EntityExplain, error) {
	start := time.Now()
	out, err := s.store.EntityExplain(ctx, repo, id)
	s.metrics.observeQuery(queryOpEntityExplain, start, err)
	return out, err
}

// SemanticMisses returns the files whose cached content_sha differs or is absent.
func (s *Service) SemanticMisses(ctx context.Context, repo string, files []FileSHA) ([]string, error) {
	start := time.Now()
	out, err := s.store.SemanticMisses(ctx, repo, files)
	s.metrics.observeQuery(queryOpSemanticMisses, start, err)
	return out, err
}

// Related returns semantic neighbors of id filtered by relations and minConfidence,
// capped at limit rows by the SQL query.
func (s *Service) Related(ctx context.Context, repo, id string, relations []string, minConfidence float64, limit int) ([]RelatedResult, error) {
	if limit <= 0 {
		limit = defaultImportantLimit
	}
	if limit > maxImportantLimit {
		limit = maxImportantLimit
	}
	start := time.Now()
	out, err := s.store.Related(ctx, repo, id, relations, minConfidence, limit)
	s.metrics.observeQuery(queryOpRelated, start, err)
	return out, err
}

// Hyperedges returns the hyperedges in repo, optionally filtered by member entity,
// capped at limit rows by the SQL query.
func (s *Service) Hyperedges(ctx context.Context, repo, entityID string, limit int) ([]Hyperedge, error) {
	if limit <= 0 {
		limit = defaultImportantLimit
	}
	if limit > maxImportantLimit {
		limit = maxImportantLimit
	}
	start := time.Now()
	out, err := s.store.Hyperedges(ctx, repo, entityID, limit)
	s.metrics.observeQuery(queryOpHyperedges, start, err)
	return out, err
}

// Hyperedge returns a single hyperedge with its members.
func (s *Service) Hyperedge(ctx context.Context, repo, id string) (Hyperedge, error) {
	start := time.Now()
	out, err := s.store.Hyperedge(ctx, repo, id)
	s.metrics.observeQuery(queryOpHyperedge, start, err)
	return out, err
}

// Communities returns the detected communities for a repo, capped at limit rows.
func (s *Service) Communities(ctx context.Context, repo string, limit int) ([]CommunityRow, error) {
	if limit <= 0 {
		limit = defaultImportantLimit
	}
	if limit > maxImportantLimit {
		limit = maxImportantLimit
	}
	start := time.Now()
	out, err := s.store.Communities(ctx, repo, limit)
	s.metrics.observeQuery(queryOpCommunities, start, err)
	return out, err
}

// Community returns the member entities of one community, capped at limit rows.
func (s *Service) Community(ctx context.Context, repo string, community, limit int) ([]Entity, error) {
	if limit <= 0 {
		limit = defaultImportantLimit
	}
	if limit > maxImportantLimit {
		limit = maxImportantLimit
	}
	start := time.Now()
	out, err := s.store.Community(ctx, repo, community, limit)
	s.metrics.observeQuery(queryOpCommunity, start, err)
	return out, err
}

// Bridges returns high-betweenness multi-community connectors, capped by limit.
func (s *Service) Bridges(ctx context.Context, repo string, limit int) ([]Bridge, error) {
	if limit <= 0 {
		limit = defaultImportantLimit
	}
	if limit > maxImportantLimit {
		limit = maxImportantLimit
	}
	start := time.Now()
	out, err := s.store.Bridges(ctx, repo, limit)
	s.metrics.observeQuery(queryOpBridges, start, err)
	return out, err
}

// ImportantEntitiesBy ranks entities by degree (default) or betweenness.
func (s *Service) ImportantEntitiesBy(ctx context.Context, repo, by string, limit int) ([]EntityDegree, error) {
	if limit <= 0 {
		limit = defaultImportantLimit
	}
	if limit > maxImportantLimit {
		limit = maxImportantLimit
	}
	start := time.Now()
	out, err := s.store.ImportantEntitiesBy(ctx, repo, by, limit)
	s.metrics.observeQuery(queryOpImportantBy, start, err)
	return out, err
}
