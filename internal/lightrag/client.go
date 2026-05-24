package lightrag

import "context"

// Client is the interface every LightRAG backend must implement.
type Client interface {
	InsertDocument(ctx context.Context, req InsertRequest) (*InsertResponse, error)
	GetDocument(ctx context.Context, id string) (*Document, error)
	DeleteDocument(ctx context.Context, id string) error

	Query(ctx context.Context, req QueryRequest) (*QueryResponse, error)
	QueryDescribe(ctx context.Context, req QueryRequest) (*DescribeResponse, error)

	ListEntities(ctx context.Context, q string) ([]Entity, error)
	GetEntity(ctx context.Context, id string) (*Entity, error)
	UpdateEntity(ctx context.Context, id string, upd EntityUpdate) (*Entity, error)

	ListEdges(ctx context.Context) ([]Edge, error)
	CreateEdge(ctx context.Context, e Edge) (*Edge, error)
	DeleteEdge(ctx context.Context, id string) error

	Health(ctx context.Context) error
}
