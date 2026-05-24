package lightrag

import "time"

// QueryMode identifies the retrieval strategy for a LightRAG query.
type QueryMode string

// Supported query modes.
const (
	QueryModeHybrid QueryMode = "hybrid"
	QueryModeLocal  QueryMode = "local"
	QueryModeGlobal QueryMode = "global"
	QueryModeNaive  QueryMode = "naive"
)

// Valid reports whether m is a recognized query mode.
func (m QueryMode) Valid() bool {
	switch m {
	case QueryModeHybrid, QueryModeLocal, QueryModeGlobal, QueryModeNaive:
		return true
	}
	return false
}

// Document is a unit of text stored in LightRAG.
type Document struct {
	ID        string            `json:"id"`
	Content   string            `json:"content"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt *time.Time        `json:"created_at,omitempty"`
}

// InsertRequest carries one or more documents to be inserted.
type InsertRequest struct {
	Documents []Document `json:"documents"`
}

// InsertResponse carries the IDs assigned to the inserted documents.
type InsertResponse struct {
	IDs []string `json:"ids"`
}

// QueryRequest describes a retrieval query.
type QueryRequest struct {
	Query string    `json:"query"`
	Mode  QueryMode `json:"mode"`
	TopK  int       `json:"top_k,omitempty"`
}

// Match is a single ranked retrieval result.
type Match struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
	Text  string  `json:"text"`
}

// QueryResponse holds ranked matches returned by a query.
type QueryResponse struct {
	Matches []Match `json:"matches"`
}

// DescribeResponse holds the generative answer and its source document IDs.
type DescribeResponse struct {
	Response string   `json:"response"`
	Sources  []string `json:"sources"`
}

// Entity is a node in the LightRAG knowledge graph.
type Entity struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Description string            `json:"description,omitempty"`
	Properties  map[string]string `json:"properties,omitempty"`
}

// EntityUpdate carries partial updates for an entity.
type EntityUpdate struct {
	Name        *string           `json:"name,omitempty"`
	Type        *string           `json:"type,omitempty"`
	Description *string           `json:"description,omitempty"`
	Properties  map[string]string `json:"properties,omitempty"`
}

// Edge is a directed relationship between two entities in the knowledge graph.
type Edge struct {
	ID         string            `json:"id"`
	FromEntity string            `json:"from_entity"`
	ToEntity   string            `json:"to_entity"`
	Relation   string            `json:"relation"`
	Properties map[string]string `json:"properties,omitempty"`
}
