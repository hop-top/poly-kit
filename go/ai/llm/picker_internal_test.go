package llm

import (
	"math/rand"
	"testing"

	"hop.top/aim"
)

// rank operates on whatever slice order it receives; aim.Registry.Models()
// pre-sorts results so the public-API determinism test cannot prove that
// sort.SliceStable is doing the work. These tests call rank() directly with
// shuffled input to lock the stability contract.

func TestRank_DeterministicOnShuffledInput(t *testing.T) {
	cases := []struct {
		name     string
		budget   BudgetTier
		wantProv string
		wantID   string
	}{
		{"cheap", BudgetCheap, "alpha", "model-a"},
		{"premium", BudgetPremium, "alpha", "model-a"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Three priced-identical models; alphabetical (Provider, ID) tiebreak
			// must always select (alpha, model-a) regardless of input order.
			base := []*aim.Model{
				{Provider: "zeta", ID: "model-z", Cost: &aim.Cost{Input: 2, Output: 2}, Limit: aim.Limits{Context: 100000}},
				{Provider: "alpha", ID: "model-a", Cost: &aim.Cost{Input: 2, Output: 2}, Limit: aim.Limits{Context: 100000}},
				{Provider: "mu", ID: "model-m", Cost: &aim.Cost{Input: 2, Output: 2}, Limit: aim.Limits{Context: 100000}},
			}

			r := rand.New(rand.NewSource(42))
			for i := 0; i < 10; i++ {
				cp := make([]*aim.Model, len(base))
				copy(cp, base)
				r.Shuffle(len(cp), func(a, b int) { cp[a], cp[b] = cp[b], cp[a] })

				got := rank(cp, tc.budget)
				if got.Provider != tc.wantProv || got.ID != tc.wantID {
					t.Fatalf("iter %d: rank picked (%s,%s), want (%s,%s); input order=%v",
						i, got.Provider, got.ID, tc.wantProv, tc.wantID, providerIDs(cp))
				}
			}
		})
	}
}

func TestRank_BalancedMedianEven(t *testing.T) {
	// Even-sized survivor lists pick survivors[len/2] — the upper-middle entry
	// after the price-asc sort. Lock this quirk so future "fix" attempts argue
	// with a test.
	base := []*aim.Model{
		{Provider: "p", ID: "cheap", Cost: &aim.Cost{Input: 1, Output: 1}},
		{Provider: "p", ID: "mid-lo", Cost: &aim.Cost{Input: 2, Output: 2}},
		{Provider: "p", ID: "mid-hi", Cost: &aim.Cost{Input: 3, Output: 3}},
		{Provider: "p", ID: "spendy", Cost: &aim.Cost{Input: 4, Output: 4}},
	}

	r := rand.New(rand.NewSource(7))
	for i := 0; i < 10; i++ {
		cp := make([]*aim.Model, len(base))
		copy(cp, base)
		r.Shuffle(len(cp), func(a, b int) { cp[a], cp[b] = cp[b], cp[a] })

		got := rank(cp, BudgetBalanced)
		// len=4 → index 2 → the third-cheapest of four.
		if got.ID != "mid-hi" {
			t.Fatalf("iter %d: balanced median = %q, want %q", i, got.ID, "mid-hi")
		}
	}

	// n=2: index 1 → the more expensive of two.
	two := []*aim.Model{
		{Provider: "p", ID: "cheaper", Cost: &aim.Cost{Input: 1, Output: 1}},
		{Provider: "p", ID: "pricier", Cost: &aim.Cost{Input: 5, Output: 5}},
	}
	got := rank(two, BudgetBalanced)
	if got.ID != "pricier" {
		t.Fatalf("balanced n=2 median = %q, want %q (the higher-priced entry)", got.ID, "pricier")
	}
}

func providerIDs(ms []*aim.Model) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Provider + "/" + m.ID
	}
	return out
}
