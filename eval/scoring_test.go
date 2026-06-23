package eval

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func m(id, text string) memory.QueryMatch {
	return memory.QueryMatch{ID: id, Text: text}
}

func TestMatchHits_TextAndID(t *testing.T) {
	require.True(t, MatchHits(m("id1", "the newest stable Go"), "newest stable Go"), "substring of Text")
	require.True(t, MatchHits(m("eval-seed-go-version", "unrelated"), "eval-seed-go-version"), "match on ID")
	require.True(t, MatchHits(m("PrefixedID-123", "x"), "prefixedid"), "case-insensitive substring of ID")
	require.True(t, MatchHits(m("id", "Use stdlib log/slog in Go"), "LOG/SLOG"), "case-insensitive substring of Text")
	require.False(t, MatchHits(m("id", "something else"), "missing phrase"))
}

func TestRecallAtK_FullPartialMiss(t *testing.T) {
	c := GoldenCase{Name: "c", Expected: []string{"alpha", "beta"}}
	matches := []memory.QueryMatch{m("1", "has alpha"), m("2", "has beta")}

	require.InDelta(t, 1.0, RecallAtK(c, matches, 10), 1e-9, "both expected hit")
	require.InDelta(t, 0.5, RecallAtK(GoldenCase{Expected: []string{"alpha", "gamma"}}, matches, 10), 1e-9, "one of two hit")
	require.InDelta(t, 0.0, RecallAtK(GoldenCase{Expected: []string{"gamma", "delta"}}, matches, 10), 1e-9, "none hit")
}

func TestRecallAtK_TruncationBoundary(t *testing.T) {
	c := GoldenCase{Expected: []string{"target"}}
	matches := []memory.QueryMatch{m("1", "noise"), m("2", "noise"), m("3", "the target here")}

	require.InDelta(t, 0.0, RecallAtK(c, matches, 2), 1e-9, "target is at rank 3, excluded by k=2")
	require.InDelta(t, 1.0, RecallAtK(c, matches, 3), 1e-9, "k=3 includes rank 3")
	require.InDelta(t, 1.0, RecallAtK(c, matches, 100), 1e-9, "k beyond len uses all matches")
}

func TestRecallAtK_EmptyMatchesAndK(t *testing.T) {
	c := GoldenCase{Expected: []string{"x"}}
	require.InDelta(t, 0.0, RecallAtK(c, nil, 10), 1e-9, "no matches -> 0")
	require.InDelta(t, 0.0, RecallAtK(c, []memory.QueryMatch{m("1", "x")}, 0), 1e-9, "k=0 considers no matches")
}

func TestMRR_RankOneVsRankThreeVsMiss(t *testing.T) {
	c := GoldenCase{Expected: []string{"target"}}
	rank1 := []memory.QueryMatch{m("1", "the target"), m("2", "noise")}
	rank3 := []memory.QueryMatch{m("1", "noise"), m("2", "noise"), m("3", "the target")}
	miss := []memory.QueryMatch{m("1", "noise"), m("2", "noise")}

	require.InDelta(t, 1.0, MRR(c, rank1), 1e-9)
	require.InDelta(t, 1.0/3.0, MRR(c, rank3), 1e-9)
	require.InDelta(t, 0.0, MRR(c, miss), 1e-9)
	require.InDelta(t, 0.0, MRR(c, nil), 1e-9)
}

func TestMRR_FirstSatisfyingAnyExpected(t *testing.T) {
	c := GoldenCase{Expected: []string{"alpha", "beta"}}
	// rank 1 satisfies beta even though alpha appears later; MRR is 1/1.
	matches := []memory.QueryMatch{m("1", "has beta"), m("2", "has alpha")}
	require.InDelta(t, 1.0, MRR(c, matches), 1e-9)
}

func TestScoreCase(t *testing.T) {
	c := GoldenCase{Name: "c", Mode: memory.QueryModeHybrid, Expected: []string{"alpha", "beta", "gamma"}}
	matches := []memory.QueryMatch{m("1", "noise"), m("2", "has alpha and beta")}
	s := ScoreCase(c, matches, 5)
	require.Equal(t, "c", s.Name)
	require.Equal(t, memory.QueryModeHybrid, s.Mode)
	require.Equal(t, 5, s.K)
	require.Equal(t, 2, s.Hits)
	require.Equal(t, 3, s.Expected)
	require.InDelta(t, 2.0/3.0, s.RecallAtK, 1e-9)
	require.InDelta(t, 1.0/2.0, s.MRR, 1e-9)
}

func TestScoreCase_RanksByScoreNotArrivalOrder(t *testing.T) {
	c := GoldenCase{Name: "c", Mode: memory.QueryModeHybrid, Expected: []string{"target"}}
	// Arrival order puts the relevant match last, but its Score is highest, so
	// score-ranking must treat it as rank 1 (the whole point of /queries:data).
	matches := []memory.QueryMatch{
		{ID: "1", Text: "noise", Score: 0.2},
		{ID: "2", Text: "noise", Score: 0.5},
		{ID: "3", Text: "the target", Score: 0.9},
	}
	require.InDelta(t, 1.0, MRR(c, matches), 1e-9, "highest-scored match ranks first, not last by arrival")
	require.InDelta(t, 1.0, RecallAtK(c, matches, 1), 1e-9, "top-1 by score includes the relevant match")

	s := ScoreCase(c, matches, 1)
	require.InDelta(t, 1.0, s.MRR, 1e-9)
	require.InDelta(t, 1.0, s.RecallAtK, 1e-9)
}

func TestSummarize(t *testing.T) {
	scores := []Score{
		{RecallAtK: 1.0, MRR: 1.0},
		{RecallAtK: 0.0, MRR: 0.0},
		{RecallAtK: 0.5, MRR: 0.25},
	}
	sum := Summarize(scores)
	require.Equal(t, 3, sum.Cases)
	require.InDelta(t, 0.5, sum.MeanRecallAtK, 1e-9)
	require.InDelta(t, 1.25/3.0, sum.MeanMRR, 1e-9)

	empty := Summarize(nil)
	require.Equal(t, 0, empty.Cases)
	require.InDelta(t, 0.0, empty.MeanRecallAtK, 1e-9)
	require.InDelta(t, 0.0, empty.MeanMRR, 1e-9)
}
