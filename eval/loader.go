// Package eval holds the memory retrieval-quality eval harness data and the
// loaders that parse and validate it. The golden cases and seed corpus live as
// JSON next to this package (eval/golden/*.json, eval/seed/*.json) and are
// embedded so the eval binary is self-contained. See README.md for the formats.
package eval

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// These mirror the service-side clamp in internal/httpapi/queries.go so golden
// cases are normalized to the same bounds the live /queries endpoint applies.
const (
	defaultTopK = 10
	maxTopK     = 500
)

//go:embed golden/*.json
var goldenFS embed.FS

//go:embed seed/*.json
var seedFS embed.FS

// GoldenCase is one (query, mode, expected) retrieval case. Expected holds
// substrings and/or memory IDs that a correct retrieval must surface in the
// returned Matches (Match.Text or Match.ID).
type GoldenCase struct {
	Name     string           `json:"name"`
	Query    string           `json:"query"`
	Mode     memory.QueryMode `json:"mode"`
	TopK     int              `json:"top_k,omitempty"`
	Expected []string         `json:"expected"`
}

// SeedItem is a memory the harness bulk-ingests before querying. It is the
// wire shape the /memories:bulk endpoint already accepts.
type SeedItem = memory.IngestItem

// LoadGolden parses and validates every embedded golden/*.json file, returning
// the combined set with normalized TopK and globally-unique names.
func LoadGolden() ([]GoldenCase, error) { return loadGolden(goldenFS, "golden") }

// LoadSeed parses and validates every embedded seed/*.json file, returning the
// combined corpus with globally-unique idempotency keys.
func LoadSeed() ([]SeedItem, error) { return loadSeed(seedFS, "seed") }

// LoadGoldenDir is LoadGolden against an on-disk directory of *.json files,
// for the runner's optional path override.
func LoadGoldenDir(dir string) ([]GoldenCase, error) { return loadGolden(os.DirFS(dir), ".") }

// LoadSeedDir is LoadSeed against an on-disk directory of *.json files.
func LoadSeedDir(dir string) ([]SeedItem, error) { return loadSeed(os.DirFS(dir), ".") }

func loadGolden(fsys fs.FS, dir string) ([]GoldenCase, error) {
	files, err := fs.Glob(fsys, path.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("eval: glob golden: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("eval: no golden files under %s", dir)
	}
	var all []GoldenCase
	seen := map[string]bool{}
	for _, f := range files {
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, fmt.Errorf("eval: read %s: %w", f, err)
		}
		cases, err := parseGolden(data)
		if err != nil {
			return nil, fmt.Errorf("eval: %s: %w", f, err)
		}
		for _, c := range cases {
			if seen[c.Name] {
				return nil, fmt.Errorf("eval: duplicate golden case name %q (across files)", c.Name)
			}
			seen[c.Name] = true
			all = append(all, c)
		}
	}
	return all, nil
}

func loadSeed(fsys fs.FS, dir string) ([]SeedItem, error) {
	files, err := fs.Glob(fsys, path.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("eval: glob seed: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("eval: no seed files under %s", dir)
	}
	var all []SeedItem
	seen := map[string]bool{}
	for _, f := range files {
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, fmt.Errorf("eval: read %s: %w", f, err)
		}
		items, err := parseSeed(data)
		if err != nil {
			return nil, fmt.Errorf("eval: %s: %w", f, err)
		}
		for _, it := range items {
			if seen[it.IdempotencyKey] {
				return nil, fmt.Errorf("eval: duplicate seed key %q (across files)", it.IdempotencyKey)
			}
			seen[it.IdempotencyKey] = true
			all = append(all, it)
		}
	}
	return all, nil
}

// parseGolden decodes one golden JSON array, normalizing and validating each
// case and rejecting duplicate names within the array.
func parseGolden(data []byte) ([]GoldenCase, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cases []GoldenCase
	if err := dec.Decode(&cases); err != nil {
		return nil, fmt.Errorf("parse golden: %w", err)
	}
	seen := map[string]bool{}
	for i := range cases {
		if err := cases[i].validate(); err != nil {
			return nil, err
		}
		cases[i].normalize()
		if seen[cases[i].Name] {
			return nil, fmt.Errorf("duplicate golden case name %q", cases[i].Name)
		}
		seen[cases[i].Name] = true
	}
	return cases, nil
}

// parseSeed decodes one seed JSON array, validating each item and rejecting
// duplicate idempotency keys within the array.
func parseSeed(data []byte) ([]SeedItem, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var items []SeedItem
	if err := dec.Decode(&items); err != nil {
		return nil, fmt.Errorf("parse seed: %w", err)
	}
	seen := map[string]bool{}
	for _, it := range items {
		if strings.TrimSpace(it.IdempotencyKey) == "" {
			return nil, fmt.Errorf("seed item missing idempotency_key")
		}
		if strings.TrimSpace(it.Text) == "" {
			return nil, fmt.Errorf("seed item %q missing text", it.IdempotencyKey)
		}
		if seen[it.IdempotencyKey] {
			return nil, fmt.Errorf("duplicate seed key %q", it.IdempotencyKey)
		}
		seen[it.IdempotencyKey] = true
	}
	return items, nil
}

// validate reports whether a golden case is well-formed. TopK==0 is allowed
// (defaulted by normalize); a negative TopK is rejected.
func (c GoldenCase) validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("golden case missing name")
	}
	if strings.TrimSpace(c.Query) == "" {
		return fmt.Errorf("golden case %q missing query", c.Name)
	}
	if !c.Mode.Valid() {
		return fmt.Errorf("golden case %q invalid mode %q", c.Name, c.Mode)
	}
	if c.TopK < 0 {
		return fmt.Errorf("golden case %q top_k must be non-negative", c.Name)
	}
	if len(c.Expected) == 0 {
		return fmt.Errorf("golden case %q missing expected", c.Name)
	}
	for _, e := range c.Expected {
		if strings.TrimSpace(e) == "" {
			return fmt.Errorf("golden case %q has a blank expected entry", c.Name)
		}
	}
	return nil
}

// normalize applies the same top_k defaulting/clamping the service does.
func (c *GoldenCase) normalize() {
	if c.TopK == 0 {
		c.TopK = defaultTopK
	}
	if c.TopK > maxTopK {
		c.TopK = maxTopK
	}
}
