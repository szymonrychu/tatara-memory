// TODO(wave-6-merge): Local types in this file must be reconciled with Wave 3A's
// internal/memory package types. At merge time, either:
//   - replace httpapi.* local types with imports from internal/memory, OR
//   - keep httpapi.* local types and have memory.Service re-export them.
//
// Search for "httpapi.Memory", "httpapi.Query", etc. across httpapi package to find all usages.
package httpapi

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

// Ingest job status values.
const (
	JobStatusQueued  JobStatus = "queued"
	JobStatusRunning JobStatus = "running"
	JobStatusDone    JobStatus = "done"
	JobStatusFailed  JobStatus = "failed"
)

// IngestJob tracks the progress of a bulk ingest operation.
type IngestJob struct {
	ID     string    `json:"id"`
	Status JobStatus `json:"status"`
	Total  int       `json:"total"`
	Done   int       `json:"done"`
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
