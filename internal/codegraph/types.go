// Package codegraph stores and serves a deterministic code-structure graph
// (entities and edges) emitted by the repo ingester, scoped per repository and
// reconciled at file granularity.
package codegraph

import (
	"errors"
	"strings"
)

// ErrEntityNotFound is returned when a requested entity does not exist.
var ErrEntityNotFound = errors.New("codegraph: entity not found")

// ErrInvalidScope is returned when a push is malformed or contains an entity or
// edge whose owning file is not in the push's declared file set.
var ErrInvalidScope = errors.New("codegraph: invalid push scope")

// Relation constants used by the named-traversal endpoints. The producer
// (sub-project B) emits the full relation vocabulary; this service only needs
// the relations it groups into traversal sets.
const (
	relCalls        = "calls"
	relImports      = "imports"
	relReferences   = "references"
	relDependsOn    = "depends_on"
	relValueRef     = "value_ref"
	relIncludes     = "includes"
	relSubchart     = "subchart"
	relModuleSource = "module_source"
)

var (
	callRelations       = []string{relCalls}
	dependencyRelations = []string{relImports, relReferences, relDependsOn}
	resourceRelations   = []string{relDependsOn, relReferences, relValueRef, relIncludes, relSubchart, relModuleSource}
)

const (
	defaultDepth = 3
	maxDepth     = 10
)

// Phase 0 entity-type constants (doc/concept/rationale nodes flow through
// Entities, not a separate array). Locked by the Phase 0 contract.
const (
	EntityDocFile    = "doc_file"
	EntityDocSection = "doc_section"
	EntityConcept    = "concept"
	EntityRationale  = "rationale"
)

// Phase 0 semantic relation constants (reserved now, emitted Phase 2).
const (
	RelConceptuallyRelated = "conceptually_related_to"
	RelSemanticallySimilar = "semantically_similar_to"
	RelRationaleFor        = "rationale_for"
	RelSharesDataWith      = "shares_data_with"
	RelCites               = "cites"
)

// Confidence tiers for code edges. Mapping (per Phase 0 lock):
// 1.0 -> EXTRACTED; (0.3,1) -> INFERRED; <=0.3 -> AMBIGUOUS.
const (
	TierExtracted = "EXTRACTED"
	TierInferred  = "INFERRED"
	TierAmbiguous = "AMBIGUOUS"
)

// ambiguousScoreThreshold is the upper boundary (inclusive) for the AMBIGUOUS tier.
// Any edge with confidence_score <= this value is treated as ambiguous.
// Shared between TierFor and AmbiguousEdges SQL filter.
const ambiguousScoreThreshold = 0.3

// TierFor maps a confidence score to its tier.
func TierFor(score float64) string {
	switch {
	case score >= 1.0:
		return TierExtracted
	case score <= ambiguousScoreThreshold:
		return TierAmbiguous
	default:
		return TierInferred
	}
}

// Entity is a node in the code graph (a package, type, function, resource,
// template, value key, file, doc, concept, or repo root). The ID is a canonical
// "<lang>:<kind>:<fqn>" string and is treated as opaque by the store.
type Entity struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Description string            `json:"description,omitempty"`
	FilePath    string            `json:"file_path"`
	LineStart   int               `json:"line_start,omitempty"`
	LineEnd     int               `json:"line_end,omitempty"`
	SourceURL   string            `json:"source_url,omitempty"`
	Author      string            `json:"author,omitempty"`
	CapturedAt  string            `json:"captured_at,omitempty"`
	Properties  map[string]string `json:"properties,omitempty"`
}

// Edge is a directed, typed relationship between two entities. SrcFile is the
// file that owns the edge (where the reference originates) and is the unit of
// file-granular replacement. ConfidenceScore/ConfidenceTier are promoted to
// typed columns on reconcile (DEFAULT 1.0/'EXTRACTED' when the producer omits).
type Edge struct {
	From            string            `json:"from"`
	To              string            `json:"to"`
	Relation        string            `json:"relation"`
	SrcFile         string            `json:"src_file"`
	ConfidenceScore float64           `json:"confidence_score,omitempty"`
	ConfidenceTier  string            `json:"confidence_tier,omitempty"`
	Properties      map[string]string `json:"properties,omitempty"`
}

// Symbol roles for cross-repo resolution.
const (
	RoleProvides = "provides"
	RoleRequires = "requires"
)

// SymbolRow is a cross-repo provides/requires fact owned by a file.
type SymbolRow struct {
	Symbol   string `json:"symbol"`
	Lang     string `json:"lang"`
	Kind     string `json:"kind"`
	Role     string `json:"role"`
	EntityID string `json:"entity_id"`
	SrcFile  string `json:"src_file"`
}

// CrossRef is one end of a cross-repo symbol link.
type CrossRef struct {
	Repo     string `json:"repo"`
	EntityID string `json:"entity_id"`
	Symbol   string `json:"symbol"`
	Lang     string `json:"lang"`
}

// CrossRepoLinks are the cross-repo consumers/providers of an entity.
type CrossRepoLinks struct {
	Consumers []CrossRef `json:"consumers"` // others requiring what this entity provides
	Providers []CrossRef `json:"providers"` // others providing what this entity requires
}

// Hyperedge is a genuinely n-ary relationship over 3+ entities, owned by SrcFile
// and reconciled per-file like edges. Empty until Phase 2.
type Hyperedge struct {
	ID              string            `json:"id"`
	Label           string            `json:"label"`
	Relation        string            `json:"relation"` // participate_in|implement|form
	ConfidenceScore float64           `json:"confidence_score,omitempty"`
	SrcFile         string            `json:"src_file"`
	Members         []string          `json:"members"` // entity IDs (3+)
	Properties      map[string]string `json:"properties,omitempty"`
}

// FileSHA is one file's content hash, used by the semantic-misses cache check
// and as the per-path value of GraphPush.FileSHAs.
type FileSHA struct {
	Path       string `json:"path"`
	ContentSHA string `json:"content_sha"`
}

// GraphPush is one ingest request: the changed file set plus the entities and
// edges those files own. Reconciliation deletes the prior graph owned by Files
// (scoped by Extractor) then inserts Entities, Edges, Symbols, and Hyperedges,
// in one transaction. When FileSHAs is set the semantic_extractions cache is
// upserted for those paths.
type GraphPush struct {
	Repo       string            `json:"repo"`
	Commit     string            `json:"commit,omitempty"`
	Extractor  string            `json:"extractor,omitempty"`
	Files      []string          `json:"files"`
	Entities   []Entity          `json:"entities"`
	Edges      []Edge            `json:"edges"`
	Symbols    []SymbolRow       `json:"symbols,omitempty"`
	Hyperedges []Hyperedge       `json:"hyperedges,omitempty"`
	FileSHAs   map[string]string `json:"file_shas,omitempty"`
}

// ExtractorAST is the default origin tag written to graph rows when a push omits
// Extractor. Reconcile scopes its per-src_file deletes by this tag.
const ExtractorAST = "ast"

// ExtractorSemantic tags rows produced by the LLM semantic extraction stage.
const ExtractorSemantic = "semantic"

// PushResult summarises a completed reconciliation.
type PushResult struct {
	Repo             string `json:"repo"`
	Files            int    `json:"files"`
	EntitiesUpserted int    `json:"entities_upserted"`
	EdgesUpserted    int    `json:"edges_upserted"`
}

// EntityDetail is an entity plus its immediate outgoing and incoming edges.
type EntityDetail struct {
	Entity
	OutEdges []Edge `json:"out_edges"`
	InEdges  []Edge `json:"in_edges"`
}

// PathNode is an entity reached during a traversal, with the depth (>=1) at
// which it was first found.
type PathNode struct {
	Entity
	Depth int `json:"depth"`
}

// EntityDegree is an entity with its computed degree (in+out edge count).
type EntityDegree struct {
	Entity
	Degree int `json:"degree"`
}

// GraphStats holds aggregate counts for a repo's code graph.
type GraphStats struct {
	Entities         int            `json:"entities"`
	Edges            int            `json:"edges"`
	EntitiesByType   map[string]int `json:"entities_by_type"`
	EdgesByRelation  map[string]int `json:"edges_by_relation"`
	EdgesByTier      map[string]int `json:"edges_by_tier"`
	IsolatedEntities int            `json:"isolated_entities"`
	ImportCycles     int            `json:"import_cycles"`
}

// NeighborEntity is a lightweight entity summary used in EntityExplain neighbors.
type NeighborEntity struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	FilePath  string `json:"file_path"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`
}

// EntityExplain is EntityDetail plus labeled in/out neighbor entities.
type EntityExplain struct {
	EntityDetail
	OutNeighbors []NeighborEntity `json:"out_neighbors"`
	InNeighbors  []NeighborEntity `json:"in_neighbors"`
}

// ConfidenceFilter is an optional filter applied to edge traversals.
// Zero values mean no filtering.
type ConfidenceFilter struct {
	MinConfidence float64
	Tier          string
}

// ValidTiers is the set of recognized confidence tier values.
var ValidTiers = map[string]bool{
	TierExtracted: true,
	TierInferred:  true,
	TierAmbiguous: true,
}

func clampDepth(d int) int {
	if d <= 0 {
		return defaultDepth
	}
	if d > maxDepth {
		return maxDepth
	}
	return d
}

func normalizeDir(dir string) string {
	if strings.ToLower(dir) == "in" {
		return "in"
	}
	return "out"
}
