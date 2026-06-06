package codegraph

import "testing"

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
