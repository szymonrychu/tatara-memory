package httpapi

import (
	"context"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// MemoryService is the domain interface for memory CRUD and query operations.
// Concrete implementation lives in internal/memory.
type MemoryService interface {
	CreateMemory(ctx context.Context, m memory.Memory) (memory.Memory, error)
	GetMemory(ctx context.Context, id string) (memory.Memory, error)
	DeleteMemory(ctx context.Context, id string) error
	Query(ctx context.Context, q memory.Query) (memory.QueryResult, error)
	Describe(ctx context.Context, q memory.Query) (memory.DescribeResult, error)
	GetEntity(ctx context.Context, id string) (memory.Entity, error)
	SearchEntities(ctx context.Context, q string) ([]memory.Entity, error)
	PatchEntity(ctx context.Context, id string, patch memory.Entity) (memory.Entity, error)
	ListEdges(ctx context.Context) ([]memory.Edge, error)
	CreateEdge(ctx context.Context, e memory.Edge) (memory.Edge, error)
	DeleteEdge(ctx context.Context, id string) error
}

// IngestService is the domain interface for bulk ingest operations.
// Concrete implementation lives in internal/ingest.
type IngestService interface {
	Enqueue(ctx context.Context, items []memory.IngestItem) (memory.IngestJob, error)
	GetJob(ctx context.Context, id string) (memory.IngestJob, error)
}
