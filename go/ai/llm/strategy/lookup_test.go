package strategy_test

import (
	"context"
	"testing"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/strategy"
)

func TestLookup(t *testing.T) {
	for _, tc := range []struct {
		name  string
		calls int
	}{
		{"self-consistency:5", 5},
		{"self-consistency:1", 1},
		{" Self-Consistency:3 ", 3},
		{"self-consistency", 5},
		{"cascade", 1}, // first stage accepts: default minimum confidence 0.8 met below
		{"cascade:3", 1},
		{"cascade:3:0.5", 1},
	} {
		run, ok := strategy.Lookup(tc.name)
		if !ok {
			t.Fatalf("%q: not found", tc.name)
		}
		var calls int
		s := func(_ context.Context, i int) (strategy.Candidate, error) {
			calls++
			return strategy.Candidate{Key: "a", Confidence: 0.9}, nil
		}
		r, err := run(s)(context.Background())
		if err != nil {
			t.Fatalf("%q: %v", tc.name, err)
		}
		if calls != tc.calls || r.Calls != tc.calls {
			t.Fatalf("%q: calls=%d, want %d", tc.name, calls, tc.calls)
		}
	}
}

func TestLookupCascadeEscalates(t *testing.T) {
	run, ok := strategy.Lookup("cascade:3:0.8")
	if !ok {
		t.Fatal("not found")
	}
	var seen []int
	s := func(_ context.Context, i int) (strategy.Candidate, error) {
		seen = append(seen, i)
		return strategy.Candidate{Key: "k", Confidence: []float64{0.2, 0.5, 0.95}[i], Response: llm.Response{}}, nil
	}
	r, err := run(s)(context.Background())
	if err != nil || r.Winner.Confidence != 0.95 || len(seen) != 3 || seen[0] != 0 || seen[2] != 2 {
		t.Fatalf("err=%v seen=%v %+v", err, seen, r)
	}
}

func TestLookupInvalid(t *testing.T) {
	for _, name := range []string{
		"", "nope", "best-of-n", "best-of-n:3", "self-consistency:", "self-consistency:0",
		"self-consistency:-2", "self-consistency:x", "self-consistency:3:4", "self-consistency:5x",
		"cascade:", "cascade:0", "cascade:2:", "cascade:2:abc", "cascade:2:1.5", "cascade:2:-0.1",
		"cascade:2:0.5:9", "cascade:2:NaN", "self-consistency:100000",
	} {
		if run, ok := strategy.Lookup(name); ok || run != nil {
			t.Errorf("%q: want not found", name)
		}
	}
}
