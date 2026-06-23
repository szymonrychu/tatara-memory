// Package cgeval holds the code-graph retrieval-quality eval harness data and
// the loaders that parse and validate it. It mirrors the memory eval harness
// (eval/, issue #41) for the /code/* surface: a synthetic fixture graph
// (seed/*.json) bulk-loaded via POST /code-graph:bulk, golden traversal cases
// (golden/*.json), and dual scoring (recall@k/MRR for ranked lookups, set
// precision/recall/F1 for deterministic traversals). See README.md.
package cgeval

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

// Case kinds. Each maps to one /code/* endpoint and a scoring mode.
const (
	KindSearch        = "search"
	KindEntity        = "entity"
	KindNeighbors     = "neighbors"
	KindCallers       = "callers"
	KindCallees       = "callees"
	KindDependents    = "dependents"
	KindDependencies  = "dependencies"
	KindResourceGraph = "resource-graph"
	KindFileImports   = "file-imports"
	KindPath          = "path"
)

// Mode is the scoring shape a case is scored under.
type Mode string

const (
	// ModeRanked scores an ordered result list by recall@k + MRR (search, path).
	ModeRanked Mode = "ranked"
	// ModeSet scores an unordered result set by precision/recall/F1 (traversals).
	ModeSet Mode = "set"
)

// GoldenCase is one (endpoint, args, expected) retrieval case. Only the args
// relevant to Kind are read; Expected holds entity IDs (or "from->to" edge keys
// for file-imports) that a correct response must surface.
type GoldenCase struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	ID        string   `json:"id,omitempty"`        // entity, callers/callees, dependents/dependencies, resource-graph, neighbors
	Q         string   `json:"q,omitempty"`         // search
	Type      string   `json:"type,omitempty"`      // search (optional type filter)
	Relation  string   `json:"relation,omitempty"`  // neighbors (comma-separated); path (relations)
	Direction string   `json:"direction,omitempty"` // neighbors (in|out)
	Depth     int      `json:"depth,omitempty"`     // traversals (optional)
	Path      string   `json:"path,omitempty"`      // file-imports
	From      string   `json:"from,omitempty"`      // path
	To        string   `json:"to,omitempty"`        // path
	MaxDepth  int      `json:"max_depth,omitempty"` // path (optional)
	Expected  []string `json:"expected"`
}

// Mode returns the scoring mode for the case's kind. Ranked for the ordered
// surfaces (search, path); set for the deterministic traversals.
func (c GoldenCase) Mode() Mode {
	switch c.Kind {
	case KindSearch, KindPath:
		return ModeRanked
	default:
		return ModeSet
	}
}

var validKinds = map[string]bool{
	KindSearch: true, KindEntity: true, KindNeighbors: true,
	KindCallers: true, KindCallees: true, KindDependents: true,
	KindDependencies: true, KindResourceGraph: true,
	KindFileImports: true, KindPath: true,
}

//go:embed golden/*.json
var goldenFS embed.FS

//go:embed seed/*.json
var seedFS embed.FS

// LoadGolden parses and validates every embedded golden/*.json file.
func LoadGolden() ([]GoldenCase, error) { return loadGolden(goldenFS, "golden") }

// LoadSeed parses and merges every embedded seed/*.json file into one GraphPush.
// Repo is left empty; the runner sets it from configuration.
func LoadSeed() (codegraph.GraphPush, error) { return loadSeed(seedFS, "seed") }

// LoadGoldenDir is LoadGolden against an on-disk directory, for the runner's
// optional path override.
func LoadGoldenDir(dir string) ([]GoldenCase, error) { return loadGolden(os.DirFS(dir), ".") }

// LoadSeedDir is LoadSeed against an on-disk directory of *.json files.
func LoadSeedDir(dir string) (codegraph.GraphPush, error) { return loadSeed(os.DirFS(dir), ".") }

func loadGolden(fsys fs.FS, dir string) ([]GoldenCase, error) {
	files, err := fs.Glob(fsys, path.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("cgeval: glob golden: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("cgeval: no golden files under %s", dir)
	}
	var all []GoldenCase
	seen := map[string]bool{}
	for _, f := range files {
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, fmt.Errorf("cgeval: read %s: %w", f, err)
		}
		cases, err := parseGolden(data)
		if err != nil {
			return nil, fmt.Errorf("cgeval: %s: %w", f, err)
		}
		for _, c := range cases {
			if seen[c.Name] {
				return nil, fmt.Errorf("cgeval: duplicate golden case name %q (across files)", c.Name)
			}
			seen[c.Name] = true
			all = append(all, c)
		}
	}
	return all, nil
}

func loadSeed(fsys fs.FS, dir string) (codegraph.GraphPush, error) {
	files, err := fs.Glob(fsys, path.Join(dir, "*.json"))
	if err != nil {
		return codegraph.GraphPush{}, fmt.Errorf("cgeval: glob seed: %w", err)
	}
	if len(files) == 0 {
		return codegraph.GraphPush{}, fmt.Errorf("cgeval: no seed files under %s", dir)
	}
	var merged codegraph.GraphPush
	seenFile := map[string]bool{}
	seenEntity := map[string]bool{}
	for _, f := range files {
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			return codegraph.GraphPush{}, fmt.Errorf("cgeval: read %s: %w", f, err)
		}
		var p codegraph.GraphPush
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return codegraph.GraphPush{}, fmt.Errorf("cgeval: parse seed %s: %w", f, err)
		}
		for _, fp := range p.Files {
			if !seenFile[fp] {
				seenFile[fp] = true
				merged.Files = append(merged.Files, fp)
			}
		}
		for _, e := range p.Entities {
			if e.ID == "" {
				return codegraph.GraphPush{}, fmt.Errorf("cgeval: seed %s: entity missing id", f)
			}
			if seenEntity[e.ID] {
				return codegraph.GraphPush{}, fmt.Errorf("cgeval: seed %s: duplicate entity id %q", f, e.ID)
			}
			seenEntity[e.ID] = true
			merged.Entities = append(merged.Entities, e)
		}
		merged.Edges = append(merged.Edges, p.Edges...)
	}
	if err := validateSeedScope(merged); err != nil {
		return codegraph.GraphPush{}, err
	}
	return merged, nil
}

// validateSeedScope mirrors the service-side scope checks (codegraph.Service.Push)
// so a malformed fixture fails at load time with a clear message rather than as a
// 400 from the live deployment.
func validateSeedScope(p codegraph.GraphPush) error {
	if len(p.Files) == 0 {
		return fmt.Errorf("cgeval: seed has no files")
	}
	if len(p.Entities) == 0 {
		return fmt.Errorf("cgeval: seed has no entities")
	}
	files := map[string]bool{}
	for _, f := range p.Files {
		files[f] = true
	}
	for _, e := range p.Entities {
		if e.FilePath != "" && !files[e.FilePath] {
			return fmt.Errorf("cgeval: entity %s file_path %q not in files", e.ID, e.FilePath)
		}
	}
	for _, e := range p.Edges {
		if !files[e.SrcFile] {
			return fmt.Errorf("cgeval: edge %s->%s src_file %q not in files", e.From, e.To, e.SrcFile)
		}
	}
	return nil
}

func parseGolden(data []byte) ([]GoldenCase, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cases []GoldenCase
	if err := dec.Decode(&cases); err != nil {
		return nil, fmt.Errorf("parse golden: %w", err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if err := c.validate(); err != nil {
			return nil, err
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("duplicate golden case name %q", c.Name)
		}
		seen[c.Name] = true
	}
	return cases, nil
}

// validate reports whether a golden case is well-formed for its kind.
func (c GoldenCase) validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("golden case missing name")
	}
	if !validKinds[c.Kind] {
		return fmt.Errorf("golden case %q invalid kind %q", c.Name, c.Kind)
	}
	if len(c.Expected) == 0 {
		return fmt.Errorf("golden case %q missing expected", c.Name)
	}
	for _, e := range c.Expected {
		if strings.TrimSpace(e) == "" {
			return fmt.Errorf("golden case %q has a blank expected entry", c.Name)
		}
	}
	if c.Depth < 0 || c.MaxDepth < 0 {
		return fmt.Errorf("golden case %q depth must be non-negative", c.Name)
	}
	if c.Direction != "" && c.Direction != "in" && c.Direction != "out" {
		return fmt.Errorf("golden case %q direction must be in or out", c.Name)
	}
	return c.validateArgs()
}

func (c GoldenCase) validateArgs() error {
	missing := func(field string) error {
		return fmt.Errorf("golden case %q (kind %s) missing %s", c.Name, c.Kind, field)
	}
	switch c.Kind {
	case KindSearch:
		if strings.TrimSpace(c.Q) == "" {
			return missing("q")
		}
	case KindEntity, KindCallers, KindCallees, KindDependents, KindDependencies, KindResourceGraph:
		if strings.TrimSpace(c.ID) == "" {
			return missing("id")
		}
	case KindNeighbors:
		if strings.TrimSpace(c.ID) == "" {
			return missing("id")
		}
		if strings.TrimSpace(c.Relation) == "" {
			return missing("relation")
		}
	case KindFileImports:
		if strings.TrimSpace(c.Path) == "" {
			return missing("path")
		}
	case KindPath:
		if strings.TrimSpace(c.From) == "" {
			return missing("from")
		}
		if strings.TrimSpace(c.To) == "" {
			return missing("to")
		}
	}
	return nil
}
