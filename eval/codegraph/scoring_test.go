package cgeval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func r(key string) Result        { return Result{Key: key} }
func rn(key, name string) Result { return Result{Key: key, Name: name} }

func TestResultHit_KeyAndName(t *testing.T) {
	require.True(t, rn("go:func:app/util.Validate", "Validate").hit("go:func:app/util.Validate"), "exact key")
	require.True(t, rn("go:func:app/util.Validate", "Validate").hit("Validate"), "exact name match")
	require.True(t, r("GO:FUNC:X").hit("go:func:x"), "case-insensitive")
	require.False(t, r("go:func:app/util.Validate").hit("util.Validate"), "partial key is not a match")
	require.False(t, r("go:func:app/util.Validate").hit("Format"))
}

func TestRecallAtK_FullPartialMiss(t *testing.T) {
	results := []Result{r("a"), r("b")}
	require.InDelta(t, 1.0, RecallAtK([]string{"a", "b"}, results, 10), 1e-9)
	require.InDelta(t, 0.5, RecallAtK([]string{"a", "c"}, results, 10), 1e-9)
	require.InDelta(t, 0.0, RecallAtK([]string{"c", "d"}, results, 10), 1e-9)
}

func TestRecallAtK_TruncationBoundary(t *testing.T) {
	results := []Result{r("noise1"), r("noise2"), r("target")}
	require.InDelta(t, 0.0, RecallAtK([]string{"target"}, results, 2), 1e-9, "rank 3 excluded by k=2")
	require.InDelta(t, 1.0, RecallAtK([]string{"target"}, results, 3), 1e-9, "k=3 includes rank 3")
	require.InDelta(t, 1.0, RecallAtK([]string{"target"}, results, 100), 1e-9, "k beyond len uses all")
}

func TestMRR_RankOneVsRankThreeVsMiss(t *testing.T) {
	require.InDelta(t, 1.0, MRR([]string{"x"}, []Result{r("x"), r("y")}), 1e-9)
	require.InDelta(t, 1.0/3.0, MRR([]string{"x"}, []Result{r("a"), r("b"), r("x")}), 1e-9)
	require.InDelta(t, 0.0, MRR([]string{"x"}, []Result{r("a"), r("b")}), 1e-9)
	require.InDelta(t, 0.0, MRR([]string{"x"}, nil), 1e-9)
}

func TestPrecisionAndF1(t *testing.T) {
	results := []Result{r("a"), r("b"), r("noise")}
	require.InDelta(t, 2.0/3.0, Precision([]string{"a", "b"}, results), 1e-9, "2 of 3 returned are relevant")
	require.InDelta(t, 0.0, Precision([]string{"a"}, nil), 1e-9, "no results -> 0")

	require.InDelta(t, 0.5, F1(0.5, 0.5), 1e-9)
	require.InDelta(t, 0.0, F1(0, 0), 1e-9)
	require.InDelta(t, 2.0*1.0*0.5/1.5, F1(1.0, 0.5), 1e-9)
}

func TestScoreCase_SetDedupesAndScoresWholeSet(t *testing.T) {
	c := GoldenCase{Name: "deps", Kind: KindDependents, Expected: []string{"a", "b"}}
	// "a" reached twice (two edges), plus an unexpected node.
	results := []Result{r("a"), r("a"), r("b"), r("extra")}
	s := ScoreCase(c, results, 10)
	require.Equal(t, ModeSet, s.Mode)
	require.Equal(t, 3, s.Returned, "deduped to a,b,extra")
	require.Equal(t, 2, s.Relevant)
	require.InDelta(t, 1.0, s.RecallAtK, 1e-9, "both expected present")
	require.InDelta(t, 2.0/3.0, s.Precision, 1e-9)
}

func TestScoreCase_RankedUsesKAndOrder(t *testing.T) {
	c := GoldenCase{Name: "search", Kind: KindSearch, Expected: []string{"target"}}
	results := []Result{r("noise"), r("noise2"), r("target")}
	s := ScoreCase(c, results, 2)
	require.Equal(t, ModeRanked, s.Mode)
	require.Equal(t, 2, s.K)
	require.InDelta(t, 0.0, s.RecallAtK, 1e-9, "target beyond k=2")
	require.InDelta(t, 1.0/3.0, s.MRR, 1e-9, "MRR over full list, rank 3")
}

func TestSummarize(t *testing.T) {
	scores := []Score{
		{RecallAtK: 1.0, MRR: 1.0, Precision: 1.0, F1: 1.0},
		{RecallAtK: 0.0, MRR: 0.0, Precision: 0.0, F1: 0.0},
		{RecallAtK: 0.5, MRR: 0.25, Precision: 0.5, F1: 0.5},
	}
	sum := Summarize(scores)
	require.Equal(t, 3, sum.Cases)
	require.InDelta(t, 0.5, sum.MeanRecallAtK, 1e-9)
	require.InDelta(t, 1.25/3.0, sum.MeanMRR, 1e-9)
	require.InDelta(t, 0.5, sum.MeanPrecision, 1e-9)

	require.Equal(t, 0, Summarize(nil).Cases)
}
