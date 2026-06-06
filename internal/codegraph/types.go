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

// Entity is a node in the code graph (a package, type, function, resource,
// template, value key, file, or repo root). The ID is a canonical
// "<lang>:<kind>:<fqn>" string and is treated as opaque by the store.
type Entity struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Description string            `json:"description,omitempty"`
	FilePath    string            `json:"file_path"`
	Properties  map[string]string `json:"properties,omitempty"`
}

// Edge is a directed, typed relationship between two entities. SrcFile is the
// file that owns the edge (where the reference originates) and is the unit of
// file-granular replacement.
type Edge struct {
	From       string            `json:"from"`
	To         string            `json:"to"`
	Relation   string            `json:"relation"`
	SrcFile    string            `json:"src_file"`
	Properties map[string]string `json:"properties,omitempty"`
}

// GraphPush is one ingest request: the changed file set plus the entities and
// edges those files own. Reconciliation deletes the prior graph owned by Files
// then inserts Entities and Edges, in one transaction.
type GraphPush struct {
	Repo     string   `json:"repo"`
	Commit   string   `json:"commit,omitempty"`
	Files    []string `json:"files"`
	Entities []Entity `json:"entities"`
	Edges    []Edge   `json:"edges"`
}

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
