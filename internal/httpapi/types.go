// TODO(wave-6-merge): Local types in this file mirror Wave 3A's internal/memory package types
// exactly so the merge subagent can do a clean drop-in replacement:
//   - replace httpapi.IngestJob / httpapi.IngestItemError / httpapi.JobStatus with imports from internal/memory, OR
//   - keep httpapi.* local types and have memory.Service re-export them.
//
// Search for "httpapi.Memory", "httpapi.Query", "httpapi.IngestJob", etc. across httpapi package to find all usages.
package httpapi

import "time"

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

// Memory is a unit of text stored in the memory graph.
type Memory struct {
	ID       string            `json:"id,omitempty"`
	Text     string            `json:"text"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Query describes a retrieval or describe request.
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

// DescribeResult holds the generative answer from a describe request.
type DescribeResult struct {
	Response string   `json:"response"`
	Sources  []string `json:"sources,omitempty"`
}

// Entity is a node in the knowledge graph.
type Entity struct {
	ID          string            `json:"id,omitempty"`
	Name        string            `json:"name,omitempty"`
	Type        string            `json:"type,omitempty"`
	Description string            `json:"description,omitempty"`
	Properties  map[string]string `json:"properties,omitempty"`
}

// Edge is a directed relationship between two entities in the knowledge graph.
type Edge struct {
	ID       string            `json:"id,omitempty"`
	From     string            `json:"from_entity"`
	To       string            `json:"to_entity"`
	Relation string            `json:"relation"`
	Props    map[string]string `json:"properties,omitempty"`
}

// IngestItem is a single item submitted for bulk ingest.
type IngestItem struct {
	Text     string            `json:"text"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// JobStatus describes the lifecycle state of an ingest job.
type JobStatus string

// Ingest job status values. Mirror memory.JobStatus exactly for clean wave-6-merge drop-in.
const (
	JobStatusQueued    JobStatus = "queued"
	JobStatusRunning   JobStatus = "running"
	JobStatusSucceeded JobStatus = "succeeded"
	JobStatusFailed    JobStatus = "failed"
	JobStatusPartial   JobStatus = "partial"
)

// IngestItemError records the failure of a single item within a job.
type IngestItemError struct {
	IdempotencyKey string `json:"idempotency_key"`
	Error          string `json:"error"`
}

// IngestJob tracks the progress of a bulk ingest operation.
// Fields mirror memory.IngestJob exactly for clean wave-6-merge drop-in.
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

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errSentinel("not found")

// ErrUpstream is returned when the upstream (lightrag) returns a client-error response (4xx).
var ErrUpstream = errSentinel("upstream error")

// ErrTransient is returned when the upstream returns a transient error (5xx or timeout).
var ErrTransient = errSentinel("transient upstream error")

type sentinelError string

func (e sentinelError) Error() string { return string(e) }

func errSentinel(s string) error { return sentinelError(s) }
