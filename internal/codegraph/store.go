package codegraph

import "context"

// Store is the persistence interface for the code graph.
type Store interface {
	Reconcile(ctx context.Context, p GraphPush) (PushResult, error)
	SearchEntities(ctx context.Context, repo, q, typ string, limit int) ([]Entity, error)
	GetEntity(ctx context.Context, repo, id string) (EntityDetail, error)
	Neighbors(ctx context.Context, repo, id string, relations []string, dir string, depth int, cf ConfidenceFilter) ([]PathNode, error)
	FileImports(ctx context.Context, repo, path string) ([]Edge, error)
	CountEntities(ctx context.Context, repo string) (int, error)
	CrossRepo(ctx context.Context, repo, id string) (CrossRepoLinks, error)
	ShortestPath(ctx context.Context, repo, fromID, toID string, relations []string, maxDepth int) ([]Entity, error)
	ImportantEntities(ctx context.Context, repo string, limit int) ([]EntityDegree, error)
	Stats(ctx context.Context, repo string) (GraphStats, error)
	AmbiguousEdges(ctx context.Context, repo string, limit int) ([]Edge, error)
	EntityExplain(ctx context.Context, repo, id string) (EntityExplain, error)
}
