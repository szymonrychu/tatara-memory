package codegraph

import (
	"encoding/json"
	"testing"
)

func TestSymbolRowTypes(t *testing.T) {
	s := SymbolRow{Symbol: "Foo", Lang: "go", Kind: "func", Role: RoleProvides, EntityID: "e1", SrcFile: "a.go"}
	if s.Symbol != "Foo" || s.Role != RoleProvides {
		t.Fatalf("SymbolRow fields wrong: %+v", s)
	}
	c := CrossRef{Repo: "r", EntityID: "e1", Symbol: "Foo", Lang: "go"}
	l := CrossRepoLinks{Consumers: []CrossRef{c}}
	if len(l.Consumers) != 1 {
		t.Fatalf("CrossRepoLinks wrong")
	}
	p := GraphPush{Repo: "r", Files: []string{"a.go"}, Symbols: []SymbolRow{s}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	// symbols must appear in JSON when non-empty
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["symbols"]; !ok {
		t.Fatal("symbols field missing from JSON")
	}

	// omitempty: no symbols field when Symbols is nil
	p2 := GraphPush{Repo: "r", Files: []string{"a.go"}}
	b2, _ := json.Marshal(p2)
	var m2 map[string]interface{}
	_ = json.Unmarshal(b2, &m2)
	if _, ok := m2["symbols"]; ok {
		t.Fatal("symbols field should be omitted when nil")
	}
}

func TestClampDepth(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero defaults", 0, defaultDepth},
		{"negative defaults", -3, defaultDepth},
		{"within range kept", 5, 5},
		{"over max clamped", 99, maxDepth},
		{"max kept", maxDepth, maxDepth},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := clampDepth(c.in); got != c.want {
				t.Fatalf("clampDepth(%d) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeDir(t *testing.T) {
	cases := map[string]string{
		"out": "out",
		"in":  "in",
		"":    "out",
		"OUT": "out",
		"bad": "out",
	}
	for in, want := range cases {
		if got := normalizeDir(in); got != want {
			t.Fatalf("normalizeDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEdgeConfidenceJSON(t *testing.T) {
	e := Edge{From: "a", To: "b", Relation: relCalls, SrcFile: "a.go", ConfidenceScore: 0.98, ConfidenceTier: TierInferred}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["confidence_score"] != 0.98 {
		t.Fatalf("confidence_score missing or wrong: %v", m["confidence_score"])
	}
	if m["confidence_tier"] != "INFERRED" {
		t.Fatalf("confidence_tier missing or wrong: %v", m["confidence_tier"])
	}

	// omitempty: zero confidence fields are dropped.
	e2 := Edge{From: "a", To: "b", Relation: relCalls, SrcFile: "a.go"}
	b2, _ := json.Marshal(e2)
	var m2 map[string]interface{}
	_ = json.Unmarshal(b2, &m2)
	if _, ok := m2["confidence_score"]; ok {
		t.Fatal("confidence_score should be omitted when zero")
	}
}

func TestEntityProvenanceJSON(t *testing.T) {
	e := Entity{ID: "doc:section:README.md#intro", Name: "intro", Type: EntityDocSection, FilePath: "README.md",
		LineStart: 1, LineEnd: 9, SourceURL: "https://x", Author: "me", CapturedAt: "2026-06-09T00:00:00Z"}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"line_start", "line_end", "source_url", "author", "captured_at"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("entity json missing %q", k)
		}
	}
}

func TestHyperedgeAndGraphPushJSON(t *testing.T) {
	h := Hyperedge{ID: "h1", Label: "trio", Relation: "form", ConfidenceScore: 1.0, SrcFile: "a.go", Members: []string{"e1", "e2", "e3"}}
	p := GraphPush{Repo: "r", Files: []string{"a.go"}, Hyperedges: []Hyperedge{h}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	if _, ok := m["hyperedges"]; !ok {
		t.Fatal("hyperedges field missing when non-empty")
	}

	// omitempty: no hyperedges field when nil.
	p2 := GraphPush{Repo: "r", Files: []string{"a.go"}}
	b2, _ := json.Marshal(p2)
	var m2 map[string]interface{}
	_ = json.Unmarshal(b2, &m2)
	if _, ok := m2["hyperedges"]; ok {
		t.Fatal("hyperedges field should be omitted when nil")
	}
}

func TestValidTiers(t *testing.T) {
	if !ValidTiers[TierExtracted] || !ValidTiers[TierInferred] || !ValidTiers[TierAmbiguous] {
		t.Fatal("ValidTiers must contain all three tier constants")
	}
	if ValidTiers["INVALID"] {
		t.Fatal("ValidTiers must not contain unknown values")
	}
}

func TestConfidenceFilterZeroIsNoOp(t *testing.T) {
	f := ConfidenceFilter{}
	if f.MinConfidence != 0 || f.Tier != "" {
		t.Fatal("zero ConfidenceFilter should have no filtering")
	}
}

func TestEntityDegreeJSON(t *testing.T) {
	ed := EntityDegree{Entity: Entity{ID: "e1", Name: "foo", Type: "go_func"}, Degree: 5}
	b, err := json.Marshal(ed)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	if m["degree"] != float64(5) {
		t.Fatalf("degree field wrong: %v", m["degree"])
	}
	if m["id"] != "e1" {
		t.Fatalf("id field wrong: %v", m["id"])
	}
}

func TestGraphStatsJSON(t *testing.T) {
	gs := GraphStats{
		Entities:         10,
		Edges:            5,
		EntitiesByType:   map[string]int{"go_func": 10},
		EdgesByRelation:  map[string]int{"calls": 5},
		EdgesByTier:      map[string]int{TierExtracted: 5},
		IsolatedEntities: 1,
		ImportCycles:     0,
	}
	b, err := json.Marshal(gs)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	if m["entities"] != float64(10) {
		t.Fatalf("entities field wrong: %v", m["entities"])
	}
	if m["isolated_entities"] != float64(1) {
		t.Fatalf("isolated_entities wrong: %v", m["isolated_entities"])
	}
}

func TestEntityExplainJSON(t *testing.T) {
	ex := EntityExplain{
		EntityDetail: EntityDetail{
			Entity:   Entity{ID: "e1", Name: "foo", Type: "go_func"},
			OutEdges: []Edge{{From: "e1", To: "e2", Relation: "calls", SrcFile: "a.go"}},
			InEdges:  nil,
		},
		OutNeighbors: []NeighborEntity{{ID: "e2", Name: "bar", Type: "go_func"}},
		InNeighbors:  []NeighborEntity{},
	}
	b, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	if _, ok := m["out_neighbors"]; !ok {
		t.Fatal("out_neighbors missing from json")
	}
	if _, ok := m["in_neighbors"]; !ok {
		t.Fatal("in_neighbors missing from json")
	}
}

func TestConfidenceFor(t *testing.T) {
	cases := []struct {
		name     string
		score    float64
		wantTier string
	}{
		{"extracted at 1.0", 1.0, TierExtracted},
		{"inferred mid", 0.98, TierInferred},
		{"inferred low boundary above ambiguous", 0.5, TierInferred},
		{"ambiguous at boundary", 0.3, TierAmbiguous},
		{"ambiguous below", 0.0, TierAmbiguous},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TierFor(c.score); got != c.wantTier {
				t.Fatalf("TierFor(%v) = %q, want %q", c.score, got, c.wantTier)
			}
		})
	}
}
