package lightrag

import "context"

// Client is the interface every LightRAG backend must implement.
//
// The shape follows LightRAG v1.4.16's API verbatim. Domain semantics
// (Memory, Entity, Edge) live in internal/memory; they translate to/from
// these primitives.
type Client interface {
	InsertText(ctx context.Context, req InsertTextRequest) (*InsertResponse, error)
	TrackStatus(ctx context.Context, trackID string) (*TrackStatusResponse, error)
	DeleteDocs(ctx context.Context, req DeleteDocRequest) (*DeleteDocByIdResponse, error)

	Query(ctx context.Context, req QueryRequest) (*QueryResponse, error)
	QueryData(ctx context.Context, req QueryRequest) (*QueryDataResponse, error)

	EntityExists(ctx context.Context, name string) (bool, error)
	CreateEntity(ctx context.Context, req EntityCreateRequest) (*EntityResponse, error)
	UpdateEntity(ctx context.Context, req EntityUpdateRequest) (*EntityResponse, error)
	DeleteEntity(ctx context.Context, req DeleteEntityRequest) error
	LabelSearch(ctx context.Context, q string) ([]string, error)
	Graph(ctx context.Context, label string, maxDepth, maxNodes int) (*KnowledgeGraph, error)

	CreateRelation(ctx context.Context, req RelationCreateRequest) (*RelationResponse, error)
	DeleteRelation(ctx context.Context, req DeleteRelationRequest) error

	Health(ctx context.Context) error
}
