package lightrag

// QueryMode identifies the retrieval strategy for a LightRAG query.
type QueryMode string

// Supported query modes (LightRAG v1.4.16 /query enum).
const (
	QueryModeLocal  QueryMode = "local"
	QueryModeGlobal QueryMode = "global"
	QueryModeHybrid QueryMode = "hybrid"
	QueryModeNaive  QueryMode = "naive"
	QueryModeMix    QueryMode = "mix"
	QueryModeBypass QueryMode = "bypass"
)

// Valid reports whether m is a recognized query mode.
func (m QueryMode) Valid() bool {
	switch m {
	case QueryModeLocal, QueryModeGlobal, QueryModeHybrid, QueryModeNaive, QueryModeMix, QueryModeBypass:
		return true
	}
	return false
}

// DocStatus enumerates LightRAG document processing states.
type DocStatus string

// Document processing states (LightRAG v1.4.16 DocStatus enum).
const (
	DocStatusPending      DocStatus = "pending"
	DocStatusProcessing   DocStatus = "processing"
	DocStatusPreprocessed DocStatus = "preprocessed"
	DocStatusProcessed    DocStatus = "processed"
	DocStatusFailed       DocStatus = "failed"
)

// InsertTextRequest is the body for POST /documents/text.
type InsertTextRequest struct {
	Text       string `json:"text"`
	FileSource string `json:"file_source,omitempty"`
}

// InsertTextsRequest is the body for POST /documents/texts (bulk).
type InsertTextsRequest struct {
	Texts       []string `json:"texts"`
	FileSources []string `json:"file_sources,omitempty"`
}

// InsertResponse is the LightRAG async-ingest response: status + track_id.
type InsertResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	TrackID string `json:"track_id"`
}

// DocStatusResponse is one document's status from /documents/track_status or /documents/paginated.
type DocStatusResponse struct {
	ID             string         `json:"id"`
	ContentSummary string         `json:"content_summary"`
	ContentLength  int            `json:"content_length"`
	Status         DocStatus      `json:"status"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
	TrackID        string         `json:"track_id,omitempty"`
	ChunksCount    int            `json:"chunks_count,omitempty"`
	ErrorMsg       string         `json:"error_msg,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	FilePath       string         `json:"file_path"`
}

// TrackStatusResponse is the body returned by GET /documents/track_status/{track_id}.
type TrackStatusResponse struct {
	TrackID       string              `json:"track_id"`
	Documents     []DocStatusResponse `json:"documents"`
	TotalCount    int                 `json:"total_count"`
	StatusSummary map[string]int      `json:"status_summary"`
}

// DeleteDocRequest is the body for DELETE /documents/delete_document.
type DeleteDocRequest struct {
	DocIDs         []string `json:"doc_ids"`
	DeleteFile     bool     `json:"delete_file,omitempty"`
	DeleteLLMCache bool     `json:"delete_llm_cache,omitempty"`
}

// DeleteDocByIdResponse is the response from DELETE /documents/delete_document.
type DeleteDocByIdResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	DocID   string `json:"doc_id"`
}

// QueryRequest is the body for POST /query and POST /query/data.
type QueryRequest struct {
	Query             string    `json:"query"`
	Mode              QueryMode `json:"mode,omitempty"`
	OnlyNeedContext   *bool     `json:"only_need_context,omitempty"`
	OnlyNeedPrompt    *bool     `json:"only_need_prompt,omitempty"`
	ResponseType      string    `json:"response_type,omitempty"`
	TopK              int       `json:"top_k,omitempty"`
	ChunkTopK         int       `json:"chunk_top_k,omitempty"`
	MaxEntityTokens   int       `json:"max_entity_tokens,omitempty"`
	MaxRelationTokens int       `json:"max_relation_tokens,omitempty"`
	MaxTotalTokens    int       `json:"max_total_tokens,omitempty"`
	HLKeywords        []string  `json:"hl_keywords,omitempty"`
	LLKeywords        []string  `json:"ll_keywords,omitempty"`
	UserPrompt        string    `json:"user_prompt,omitempty"`
	EnableRerank      *bool     `json:"enable_rerank,omitempty"`
	IncludeReferences *bool     `json:"include_references,omitempty"`
	IncludeChunks     *bool     `json:"include_chunk_content,omitempty"`
	Stream            *bool     `json:"stream,omitempty"`
}

// ReferenceItem is one entry in QueryResponse.References.
type ReferenceItem struct {
	ReferenceID string   `json:"reference_id"`
	FilePath    string   `json:"file_path"`
	Content     []string `json:"content,omitempty"`
}

// QueryResponse is the body returned by POST /query.
type QueryResponse struct {
	Response   string          `json:"response"`
	References []ReferenceItem `json:"references,omitempty"`
}

// QueryDataResponse is the body returned by POST /query/data.
type QueryDataResponse struct {
	Status   string         `json:"status"`
	Message  string         `json:"message"`
	Data     map[string]any `json:"data"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// EntityCreateRequest is the body for POST /graph/entity/create.
type EntityCreateRequest struct {
	EntityName string         `json:"entity_name"`
	EntityData map[string]any `json:"entity_data"`
}

// EntityUpdateRequest is the body for POST /graph/entity/edit.
type EntityUpdateRequest struct {
	EntityName  string         `json:"entity_name"`
	UpdatedData map[string]any `json:"updated_data"`
	AllowRename bool           `json:"allow_rename,omitempty"`
	AllowMerge  bool           `json:"allow_merge,omitempty"`
}

// EntityResponse is the response body for entity create/edit operations.
// Fields outside the data envelope mirror lightrag's response shape; the
// `data` map is free-form and carries entity properties.
type EntityResponse struct {
	Status           string         `json:"status"`
	Message          string         `json:"message"`
	Data             map[string]any `json:"data"`
	OperationSummary map[string]any `json:"operation_summary,omitempty"`
}

// EntityExistsResponse is the body returned by GET /graph/entity/exists.
type EntityExistsResponse struct {
	Exists bool `json:"exists"`
}

// DeleteEntityRequest is the body for DELETE /documents/delete_entity.
type DeleteEntityRequest struct {
	EntityName string `json:"entity_name"`
}

// RelationCreateRequest is the body for POST /graph/relation/create.
type RelationCreateRequest struct {
	SourceEntity string         `json:"source_entity"`
	TargetEntity string         `json:"target_entity"`
	RelationData map[string]any `json:"relation_data"`
}

// RelationUpdateRequest is the body for POST /graph/relation/edit.
type RelationUpdateRequest struct {
	SourceID    string         `json:"source_id"`
	TargetID    string         `json:"target_id"`
	UpdatedData map[string]any `json:"updated_data"`
}

// DeleteRelationRequest is the body for DELETE /documents/delete_relation.
type DeleteRelationRequest struct {
	SourceEntity string `json:"source_entity"`
	TargetEntity string `json:"target_entity"`
}

// RelationResponse mirrors the entity-edit response shape used by relation endpoints.
type RelationResponse struct {
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
}

// KnowledgeGraph is the body returned by GET /graphs.
type KnowledgeGraph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// GraphNode is one node of a KnowledgeGraph.
type GraphNode struct {
	ID         string         `json:"id"`
	Labels     []string       `json:"labels,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
}

// GraphEdge is one edge of a KnowledgeGraph.
type GraphEdge struct {
	ID         string         `json:"id,omitempty"`
	Source     string         `json:"source"`
	Target     string         `json:"target"`
	Type       string         `json:"type,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
}
