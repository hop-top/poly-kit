package hay

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scoreLen(query string, item string) int {
	if len(item) == 0 {
		return 0
	}
	// simple: longer items score higher if they contain query as substring
	for i := range item {
		if i+len(query) <= len(item) && item[i:i+len(query)] == query {
			return 100 - len(item) // shorter path = higher score
		}
	}
	return 0
}

func scoreFixed(scores map[string]int) ScoreFn[string] {
	return func(_ string, item string) int {
		return scores[item]
	}
}

func TestResolve_UniqueWinner(t *testing.T) {
	corpus := []string{"alpha", "beta", "gamma"}
	opts := Options[string]{
		Score: scoreFixed(map[string]int{"alpha": 50, "beta": 30, "gamma": 10}),
	}

	r, err := Resolve("x", corpus, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Winner != "alpha" {
		t.Errorf("winner = %q, want alpha", r.Winner)
	}
	if r.Ambiguous {
		t.Error("should not be ambiguous")
	}
}

func TestResolve_NoMatch(t *testing.T) {
	corpus := []string{"alpha", "beta"}
	opts := Options[string]{
		Score: func(_ string, _ string) int { return 0 },
	}

	_, err := Resolve("x", corpus, opts)
	var noMatch *ErrNoMatch
	if !errors.As(err, &noMatch) {
		t.Fatalf("expected ErrNoMatch, got %v", err)
	}
	if noMatch.Query != "x" {
		t.Errorf("query = %q, want x", noMatch.Query)
	}
}

func TestResolve_StaleFiltering(t *testing.T) {
	corpus := []string{"good", "stale1", "stale2"}
	opts := Options[string]{
		Score: scoreFixed(map[string]int{"good": 50, "stale1": 80, "stale2": 90}),
		Stale: func(item string) bool { return len(item) >= 5 && item[:5] == "stale" },
	}

	r, err := Resolve("x", corpus, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Winner != "good" {
		t.Errorf("winner = %q, want good", r.Winner)
	}
	if len(r.Stale) != 2 {
		t.Errorf("stale count = %d, want 2", len(r.Stale))
	}
}

func TestResolve_NoMatchWithStale(t *testing.T) {
	corpus := []string{"stale1"}
	opts := Options[string]{
		Score: func(_ string, _ string) int { return 50 },
		Stale: func(_ string) bool { return true },
	}

	_, err := Resolve("x", corpus, opts)
	var noMatch *ErrNoMatch
	if !errors.As(err, &noMatch) {
		t.Fatalf("expected ErrNoMatch, got %v", err)
	}
	if noMatch.Stale != 1 {
		t.Errorf("stale = %d, want 1", noMatch.Stale)
	}
}

func TestResolve_AmbiguousListFail(t *testing.T) {
	corpus := []string{"a", "b"}
	opts := Options[string]{
		Score:     scoreFixed(map[string]int{"a": 50, "b": 49}),
		TieMargin: 5,
		Policy:    Policy{Action: ActionList, Fail: true},
	}

	_, err := Resolve("x", corpus, opts)
	var ambig *ErrAmbiguous[string]
	if !errors.As(err, &ambig) {
		t.Fatalf("expected ErrAmbiguous, got %v", err)
	}
	if len(ambig.Candidates) != 2 {
		t.Errorf("candidates = %d, want 2", len(ambig.Candidates))
	}
}

func TestResolve_AmbiguousListNoFail(t *testing.T) {
	corpus := []string{"a", "b"}
	opts := Options[string]{
		Score:     scoreFixed(map[string]int{"a": 50, "b": 49}),
		TieMargin: 5,
		Policy:    Policy{Action: ActionList, Fail: false},
	}

	r, err := Resolve("x", corpus, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.Ambiguous {
		t.Error("should be ambiguous")
	}
	if r.Winner != "a" {
		t.Errorf("winner = %q, want a", r.Winner)
	}
}

func TestResolve_AmbiguousPickFail(t *testing.T) {
	corpus := []string{"a", "b"}
	opts := Options[string]{
		Score:     scoreFixed(map[string]int{"a": 50, "b": 49}),
		TieMargin: 5,
		Policy:    Policy{Action: ActionPick, Fail: true},
	}

	r, err := Resolve("x", corpus, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.Ambiguous {
		t.Error("should be ambiguous")
	}
	if r.Winner != "a" {
		t.Errorf("winner = %q, want a", r.Winner)
	}
}

func TestResolve_AmbiguousPickNoFail(t *testing.T) {
	corpus := []string{"a", "b"}
	opts := Options[string]{
		Score:     scoreFixed(map[string]int{"a": 50, "b": 49}),
		TieMargin: 5,
		Policy:    Policy{Action: ActionPick, Fail: false},
	}

	r, err := Resolve("x", corpus, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Ambiguous {
		t.Error("should not be ambiguous with pick+no-fail")
	}
	if r.Winner != "a" {
		t.Errorf("winner = %q, want a", r.Winner)
	}
}

func TestResolve_Bonus(t *testing.T) {
	corpus := []string{"a", "b"}
	opts := Options[string]{
		Score: scoreFixed(map[string]int{"a": 40, "b": 50}),
		Bonus: func(item string) int {
			if item == "a" {
				return 20
			}
			return 0
		},
	}

	r, err := Resolve("x", corpus, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Winner != "a" {
		t.Errorf("winner = %q, want a (bonus should push it ahead)", r.Winner)
	}
}

func TestResolve_MaxCandidates(t *testing.T) {
	corpus := make([]string, 20)
	scores := make(map[string]int, 20)
	for i := range corpus {
		corpus[i] = string(rune('a' + i))
		scores[corpus[i]] = 100 - i
	}

	opts := Options[string]{
		Score:         scoreFixed(scores),
		MaxCandidates: 3,
	}

	r, err := Resolve("x", corpus, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.Candidates) != 3 {
		t.Errorf("candidates = %d, want 3", len(r.Candidates))
	}
}

func TestResolve_TieMarginExact(t *testing.T) {
	corpus := []string{"a", "b"}
	opts := Options[string]{
		Score:     scoreFixed(map[string]int{"a": 50, "b": 45}),
		TieMargin: 5, // gap is exactly 5, not < 5
	}

	r, err := Resolve("x", corpus, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Ambiguous {
		t.Error("gap equals margin, should not be ambiguous")
	}
}

func TestResolve_EmptyCorpus(t *testing.T) {
	opts := Options[string]{
		Score: func(_ string, _ string) int { return 0 },
	}

	_, err := Resolve("x", nil, opts)
	var noMatch *ErrNoMatch
	if !errors.As(err, &noMatch) {
		t.Fatalf("expected ErrNoMatch, got %v", err)
	}
}

func TestErrorMessages(t *testing.T) {
	t.Run("ErrNoMatch", func(t *testing.T) {
		e := &ErrNoMatch{Query: "foo"}
		if e.Error() != `hay: no match for "foo"` {
			t.Errorf("unexpected message: %s", e.Error())
		}
	})

	t.Run("ErrNoMatch/stale", func(t *testing.T) {
		e := &ErrNoMatch{Query: "foo", Stale: 3}
		if e.Error() != `hay: no match for "foo" (3 stale entries skipped)` {
			t.Errorf("unexpected message: %s", e.Error())
		}
	})

	t.Run("ErrVanished", func(t *testing.T) {
		e := &ErrVanished{Query: "foo", Path: "/tmp/gone"}
		want := `hay: matched file disappeared during lookup for "foo": /tmp/gone`
		if e.Error() != want {
			t.Errorf("unexpected message: %s", e.Error())
		}
	})

	t.Run("ErrAmbiguous", func(t *testing.T) {
		e := &ErrAmbiguous[string]{Query: "foo", Candidates: []Scored[string]{{}, {}}}
		want := `hay: "foo" matches 2 candidates`
		if e.Error() != want {
			t.Errorf("unexpected message: %s", e.Error())
		}
	})
}

// --- Scorer tests ---

func TestSubsequence_MatchAndMiss(t *testing.T) {
	assert.Greater(t, Subsequence("itl", "idea/tlc"), 0)
	assert.Equal(t, 0, Subsequence("itl", "foo/bar"))
}

func TestSubsequence_WordBoundaryBonus(t *testing.T) {
	withBoundary := Subsequence("tlc", "idea/tlc")
	noBoundary := Subsequence("tlc", "ideatlc")
	assert.Greater(t, withBoundary, noBoundary)
}

func TestSubstring_HigherThanSubsequence(t *testing.T) {
	sub := Substring("tlc", "idea/tlc")
	seq := Subsequence("tlc", "idea/tlc")
	require.Greater(t, sub, 0)
	assert.Greater(t, sub, seq)
}

func TestSubstring_StartBonus(t *testing.T) {
	atStart := Substring("idea", "idea/tlc")
	notStart := Substring("idea", "x/idea/tlc")
	assert.Greater(t, atStart, notStart)
}

func TestLevenshtein_ExactAndMismatch(t *testing.T) {
	exact := Levenshtein("tlc", "tlc")
	assert.Greater(t, exact, 0)
	assert.Equal(t, 0, Levenshtein("tlc", "xyz"))
}

func TestCombined_MaxOfBoth(t *testing.T) {
	c := Combined("tlc", "idea/tlc")
	a := Subsequence("tlc", "idea/tlc")
	b := Substring("tlc", "idea/tlc")
	expected := a
	if b > a {
		expected = b
	}
	assert.Equal(t, expected, c)
}

func TestStringScore_WrapsScorer(t *testing.T) {
	type entry struct{ path string }
	fn := StringScore(func(e entry) string { return e.path }, Substring)
	score := fn("tlc", entry{"idea/tlc"})
	assert.Greater(t, score, 0)
	assert.Equal(t, 0, fn("zzz", entry{"idea/tlc"}))
}
