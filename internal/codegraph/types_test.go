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
