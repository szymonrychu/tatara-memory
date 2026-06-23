package cgeval

import "strings"

// Result is one returned item, reduced to the strings an expected entry may
// match against: Key is the entity ID (or "from->to" edge key), Name is the
// optional symbol name (set for search/path/traversal nodes, empty for edges).
type Result struct {
	Key  string
	Name string
}

// hit reports whether expected is satisfied by this result: a case-insensitive
// exact match of the Key (a canonical entity ID or "from->to" edge key) or the
// Name (a symbol name). Exact match keeps set precision honest - a substring
// rule would let an unrelated entity count as relevant when one ID is contained
// in another.
func (r Result) hit(expected string) bool {
	return strings.EqualFold(r.Key, expected) || (r.Name != "" && strings.EqualFold(r.Name, expected))
}

// Score holds the retrieval scores for a single golden case. Ranked cases score
// by recall@k + MRR; set cases by precision/recall/F1 (recall over the whole
// returned set). All four are computed for every case; the headline differs by
// mode but the aggregate gate is mean recall@k across all cases.
type Score struct {
	Name      string
	Kind      string
	Mode      Mode
	K         int // effective cutoff: configured k for ranked, |results| for set
	Expected  int // len(case.Expected)
	Returned  int // number of (deduped, for set) results returned
	Relevant  int // returned results matching some expected entry
	Hits      int // expected entries satisfied within the cutoff window
	RecallAtK float64
	MRR       float64
	Precision float64
	F1        float64
}

// Summary holds aggregate means across a set of case scores.
type Summary struct {
	Cases         int
	MeanRecallAtK float64
	MeanMRR       float64
	MeanPrecision float64
	MeanF1        float64
}

// ScoreCase computes the full Score for one case against its results. For set
// cases the results are deduped by Key and the recall window is the whole set;
// for ranked cases order is preserved and the window is the first k.
func ScoreCase(c GoldenCase, results []Result, k int) Score {
	mode := c.Mode()
	if mode == ModeSet {
		results = dedupe(results)
		k = len(results)
	}
	window := topK(results, k)
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
	relevant := relevantCount(c.Expected, results)
	precision := 0.0
	if len(results) > 0 {
		precision = float64(relevant) / float64(len(results))
	}
	return Score{
		Name:      c.Name,
		Kind:      c.Kind,
		Mode:      mode,
		K:         k,
		Expected:  len(c.Expected),
		Returned:  len(results),
		Relevant:  relevant,
		Hits:      hits,
		RecallAtK: recall,
		MRR:       mrr(c.Expected, results),
		Precision: precision,
		F1:        f1(precision, recall),
	}
}

// RecallAtK is the fraction of expected entries found within the first k results.
func RecallAtK(expected []string, results []Result, k int) float64 {
	if len(expected) == 0 {
		return 0
	}
	window := topK(results, k)
	hits := 0
	for _, e := range expected {
		if anyHits(window, e) {
			hits++
		}
	}
	return float64(hits) / float64(len(expected))
}

// MRR is the reciprocal rank of the first result satisfying any expected entry.
func MRR(expected []string, results []Result) float64 { return mrr(expected, results) }

// Precision is the fraction of returned results that satisfy some expected entry.
func Precision(expected []string, results []Result) float64 {
	if len(results) == 0 {
		return 0
	}
	return float64(relevantCount(expected, results)) / float64(len(results))
}

// F1 is the harmonic mean of precision and recall, 0 when both are 0.
func F1(precision, recall float64) float64 { return f1(precision, recall) }

// Summarize computes the aggregate means across the given case scores.
func Summarize(scores []Score) Summary {
	if len(scores) == 0 {
		return Summary{}
	}
	var recall, mrrSum, prec, f1Sum float64
	for _, s := range scores {
		recall += s.RecallAtK
		mrrSum += s.MRR
		prec += s.Precision
		f1Sum += s.F1
	}
	n := float64(len(scores))
	return Summary{
		Cases:         len(scores),
		MeanRecallAtK: recall / n,
		MeanMRR:       mrrSum / n,
		MeanPrecision: prec / n,
		MeanF1:        f1Sum / n,
	}
}

func mrr(expected []string, results []Result) float64 {
	for i, r := range results {
		for _, e := range expected {
			if r.hit(e) {
				return 1.0 / float64(i+1)
			}
		}
	}
	return 0
}

func relevantCount(expected []string, results []Result) int {
	n := 0
	for _, r := range results {
		for _, e := range expected {
			if r.hit(e) {
				n++
				break
			}
		}
	}
	return n
}

func f1(precision, recall float64) float64 {
	if precision+recall == 0 {
		return 0
	}
	return 2 * precision * recall / (precision + recall)
}

func anyHits(results []Result, expected string) bool {
	for _, r := range results {
		if r.hit(expected) {
			return true
		}
	}
	return false
}

// dedupe removes results with a repeated Key, preserving first-seen order, so a
// set query that reaches the same entity by two edges counts it once.
func dedupe(results []Result) []Result {
	seen := make(map[string]bool, len(results))
	out := make([]Result, 0, len(results))
	for _, r := range results {
		if seen[r.Key] {
			continue
		}
		seen[r.Key] = true
		out = append(out, r)
	}
	return out
}

// topK returns the first k results; k<0 or k>=len means all of them.
func topK(results []Result, k int) []Result {
	if k >= 0 && k < len(results) {
		return results[:k]
	}
	return results
}
