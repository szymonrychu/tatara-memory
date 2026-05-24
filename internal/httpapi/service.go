package httpapi

import "context"

// MemoryService is the domain interface for memory CRUD and query operations.
// At wave-6-merge, the concrete implementation will come from internal/memory.
type MemoryService interface {
	CreateMemory(ctx context.Context, m Memory) (Memory, error)
	GetMemory(ctx context.Context, id string) (Memory, error)
	DeleteMemory(ctx context.Context, id string) error
	Query(ctx context.Context, q Query) (QueryResult, error)
	Describe(ctx context.Context, q Query) (DescribeResult, error)
	GetEntity(ctx context.Context, id string) (Entity, error)
	SearchEntities(ctx context.Context, q string) ([]Entity, error)
	PatchEntity(ctx context.Context, id string, patch Entity) (Entity, error)
	ListEdges(ctx context.Context) ([]Edge, error)
	CreateEdge(ctx context.Context, e Edge) (Edge, error)
	DeleteEdge(ctx context.Context, id string) error
}

// IngestService is the domain interface for bulk ingest operations.
// At wave-6-merge, the concrete implementation will come from internal/ingest.
type IngestService interface {
	Enqueue(ctx context.Context, items []IngestItem) (IngestJob, error)
	GetJob(ctx context.Context, id string) (IngestJob, error)
}
