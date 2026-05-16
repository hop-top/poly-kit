package hay

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveStaged_FirstStageHits(t *testing.T) {
	stages := []Stage[string]{
		{
			Name:   "exact",
			Lookup: func(q string) []string { return []string{"exact-" + q} },
		},
		{
			Name:   "fuzzy",
			Lookup: func(_ string) []string { return []string{"fuzzy-match"} },
		},
	}

	opts := Options[string]{
		Score: func(_ string, _ string) int { return 50 },
	}

	r, err := ResolveStaged("foo", stages, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Winner != "exact-foo" {
		t.Errorf("winner = %q, want exact-foo", r.Winner)
	}
}

func TestResolveStaged_FallsThrough(t *testing.T) {
	stages := []Stage[string]{
		{
			Name:   "exact",
			Lookup: func(_ string) []string { return nil },
		},
		{
			Name:   "fuzzy",
			Lookup: func(_ string) []string { return []string{"fuzzy-match"} },
		},
	}

	opts := Options[string]{
		Score: func(_ string, _ string) int { return 50 },
	}

	r, err := ResolveStaged("foo", stages, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Winner != "fuzzy-match" {
		t.Errorf("winner = %q, want fuzzy-match", r.Winner)
	}
}

func TestResolveStaged_NoStageHits(t *testing.T) {
	stages := []Stage[string]{
		{Name: "a", Lookup: func(_ string) []string { return nil }},
		{Name: "b", Lookup: func(_ string) []string { return nil }},
	}

	opts := Options[string]{
		Score: func(_ string, _ string) int { return 0 },
	}

	_, err := ResolveStaged("foo", stages, opts)
	var noMatch *ErrNoMatch
	if !errors.As(err, &noMatch) {
		t.Fatalf("expected ErrNoMatch, got %v", err)
	}
}

func TestResolveStaged_EmptyStages(t *testing.T) {
	opts := Options[string]{
		Score: func(_ string, _ string) int { return 0 },
	}

	_, err := ResolveStaged("foo", nil, opts)
	var noMatch *ErrNoMatch
	if !errors.As(err, &noMatch) {
		t.Fatalf("expected ErrNoMatch, got %v", err)
	}
}

func TestResolveStaged_AmbiguityFromStage(t *testing.T) {
	stages := []Stage[string]{
		{
			Name:   "fuzzy",
			Lookup: func(_ string) []string { return []string{"a", "b"} },
		},
	}

	opts := Options[string]{
		Score:     scoreFixed(map[string]int{"a": 50, "b": 49}),
		TieMargin: 5,
		Policy:    Policy{Action: ActionList, Fail: true},
	}

	_, err := ResolveStaged("foo", stages, opts)
	var ambig *ErrAmbiguous[string]
	if !errors.As(err, &ambig) {
		t.Fatalf("expected ErrAmbiguous, got %v", err)
	}
}

func TestResolveStaged_ShortCircuit(t *testing.T) {
	called := false
	stages := []Stage[string]{
		{
			Name:   "first",
			Lookup: func(_ string) []string { return []string{"hit"} },
		},
		{
			Name: "second",
			Lookup: func(_ string) []string {
				called = true
				return []string{"should-not-reach"}
			},
		},
	}
	opts := Options[string]{
		Score: func(_ string, _ string) int { return 50 },
	}

	r, err := ResolveStaged("q", stages, opts)
	require.NoError(t, err)
	assert.Equal(t, "hit", r.Winner)
	assert.False(t, called, "second stage should not be called")
}

func TestResolveStaged_StaleInCorpus(t *testing.T) {
	stages := []Stage[string]{
		{
			Name: "all",
			Lookup: func(_ string) []string {
				return []string{"live-a", "stale-b", "live-c"}
			},
		},
	}
	opts := Options[string]{
		Score: scoreFixed(map[string]int{
			"live-a": 40, "stale-b": 90, "live-c": 60,
		}),
		Stale: func(item string) bool {
			return len(item) >= 5 && item[:5] == "stale"
		},
	}

	r, err := ResolveStaged("q", stages, opts)
	require.NoError(t, err)
	assert.Equal(t, "live-c", r.Winner)
	require.Len(t, r.Stale, 1)
	assert.Equal(t, "stale-b", r.Stale[0])
}
