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
