package eval

import (
	"strings"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// Score holds the retrieval scores for a single golden case.
type Score struct {
	Name      string
	Mode      memory.QueryMode
	K         int
	Hits      int     // expected entries hit within the first K matches
	Expected  int     // len(case.Expected)
	RecallAtK float64 // Hits / Expected
	MRR       float64 // reciprocal rank of the first satisfying match
}

// Summary holds aggregate means across a set of case scores.
type Summary struct {
	Cases         int
	MeanRecallAtK float64
	MeanMRR       float64
}

// MatchHits reports whether the expected entry is satisfied by a match: a
// case-insensitive substring of Match.Text or of Match.ID. Score is unavailable
// (translate.go hard-codes 0), so a hit is purely about content presence.
func MatchHits(match memory.QueryMatch, expected string) bool {
	return containsFold(match.Text, expected) || containsFold(match.ID, expected)
}

// RecallAtK is the fraction of the case's expected entries found within the
// first k matches (match order is the only ranking signal available).
func RecallAtK(c GoldenCase, matches []memory.QueryMatch, k int) float64 {
	if len(c.Expected) == 0 {
		return 0
	}
	window := topK(matches, k)
	hits := 0
	for _, e := range c.Expected {
		if anyHits(window, e) {
			hits++
		}
	}
	return float64(hits) / float64(len(c.Expected))
}

// MRR is the reciprocal rank (1/rank) of the first match satisfying any of the
// case's expected entries, or 0 when none match.
func MRR(c GoldenCase, matches []memory.QueryMatch) float64 {
	for i, match := range matches {
		for _, e := range c.Expected {
			if MatchHits(match, e) {
				return 1.0 / float64(i+1)
			}
		}
	}
	return 0
}

// ScoreCase computes the full Score for one case against its matches at cutoff k.
func ScoreCase(c GoldenCase, matches []memory.QueryMatch, k int) Score {
	window := topK(matches, k)
	hits := 0
	for _, e := range c.Expected {
		if anyHits(window, e) {
			hits++
		}
	}
	recall := 0.0
	if len(c.Expected) > 0 {
		recall = float64(hits) / float64(len(c.Expected))
	}
	return Score{
		Name:      c.Name,
		Mode:      c.Mode,
		K:         k,
		Hits:      hits,
		Expected:  len(c.Expected),
		RecallAtK: recall,
		MRR:       MRR(c, matches),
	}
}

// Summarize computes mean recall@k and mean MRR across the given case scores.
func Summarize(scores []Score) Summary {
	if len(scores) == 0 {
		return Summary{}
	}
	var recall, mrr float64
	for _, s := range scores {
		recall += s.RecallAtK
		mrr += s.MRR
	}
	n := float64(len(scores))
	return Summary{Cases: len(scores), MeanRecallAtK: recall / n, MeanMRR: mrr / n}
}

func anyHits(matches []memory.QueryMatch, expected string) bool {
	for _, match := range matches {
		if MatchHits(match, expected) {
			return true
		}
	}
	return false
}

// topK returns the first k matches; k<0 or k>=len means all of them.
func topK(matches []memory.QueryMatch, k int) []memory.QueryMatch {
	if k >= 0 && k < len(matches) {
		return matches[:k]
	}
	return matches
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
