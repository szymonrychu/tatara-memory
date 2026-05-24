// Package memory defines domain types and the Memory service for tatara-memory.
package memory

import "time"

// Memory is the top-level domain object stored and retrieved by the service.
type Memory struct {
	ID        string            `json:"id"`
	Text      string            `json:"text"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// Entity is a node in the knowledge graph.
type Entity struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Description string            `json:"description,omitempty"`
	Properties  map[string]string `json:"properties,omitempty"`
}

// Edge is a directed relationship between two entities in the knowledge graph.
type Edge struct {
	ID         string            `json:"id"`
	From       string            `json:"from_entity"`
	To         string            `json:"to_entity"`
	Relation   string            `json:"relation"`
	Properties map[string]string `json:"properties,omitempty"`
}

// QueryMode identifies the retrieval strategy for a query.
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

// Query carries the parameters for a retrieval or describe request.
type Query struct {
	Mode QueryMode `json:"mode"`
	Text string    `json:"text"`
	TopK int       `json:"top_k,omitempty"`
}

// QueryMatch is a single ranked retrieval result.
type QueryMatch struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
	Text  string  `json:"text"`
}

// QueryResult holds ranked matches returned by a query.
type QueryResult struct {
	Matches []QueryMatch `json:"matches"`
}

// DescribeResult holds the generative answer and its source document IDs.
type DescribeResult struct {
	Response string   `json:"response"`
	Sources  []string `json:"sources,omitempty"`
}

// JobStatus represents the lifecycle state of an ingest job.
type JobStatus string

// Ingest job lifecycle states.
const (
	JobStatusQueued    JobStatus = "queued"
	JobStatusRunning   JobStatus = "running"
	JobStatusSucceeded JobStatus = "succeeded"
	JobStatusFailed    JobStatus = "failed"
	JobStatusPartial   JobStatus = "partial"
)

// Terminal reports whether the status is a terminal state.
func (s JobStatus) Terminal() bool {
	switch s {
	case JobStatusSucceeded, JobStatusFailed, JobStatusPartial:
		return true
	}
	return false
}

// IngestItemError records the failure of a single item within a job.
type IngestItemError struct {
	IdempotencyKey string `json:"idempotency_key"`
	Error          string `json:"error"`
}

// IngestJob tracks the state of a batch ingest operation.
type IngestJob struct {
	ID        string            `json:"id"`
	Status    JobStatus         `json:"status"`
	Total     int               `json:"total"`
	Done      int               `json:"done"`
	Failed    int               `json:"failed"`
	Errors    []IngestItemError `json:"errors,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// IngestItem is a single unit of text submitted for ingestion.
type IngestItem struct {
	IdempotencyKey string            `json:"idempotency_key"`
	Text           string            `json:"text"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}
