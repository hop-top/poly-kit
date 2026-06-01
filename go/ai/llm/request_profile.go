// Consumer-facing inputs to the provider picker. Kept in their own file so a
// future picker implementation can evolve independently of the input contract.

package llm

import (
	"fmt"
	"strings"

	"hop.top/aim"
)

// RequestProfile is the consumer-facing input to PickProvider.
//
// Consumers derive the profile from invocation context (which messages,
// which tools, expected response size). The llm package does NOT infer
// it — the layering rule is: consumer derives the profile, kit picks
// the provider.
type RequestProfile struct {
	// Filter constrains which models qualify. Tristate bool fields use
	// *bool (nil = no filter). Modality slices use subset match.
	Filter aim.Filter
	// MaxInputTokens is the upper bound on prompt tokens; pickers reject
	// models whose context window is smaller. Zero = no filter.
	MaxInputTokens int
	// MaxOutputTokens is the requested response budget; pickers reject
	// models whose configured output limit is smaller. Zero = no filter.
	MaxOutputTokens int
}

// BudgetTier categorizes the cost/capability trade-off the picker should
// make. Three tiers keep the consumer-facing surface stable as upstream
// pricing changes.
type BudgetTier int

const (
	BudgetCheap BudgetTier = iota
	BudgetBalanced
	BudgetPremium
)

// String returns the canonical lowercase label for b, or "unknown" when b
// is outside the declared range.
func (b BudgetTier) String() string {
	switch b {
	case BudgetCheap:
		return "cheap"
	case BudgetBalanced:
		return "balanced"
	case BudgetPremium:
		return "premium"
	default:
		return "unknown"
	}
}

// ParseBudgetTier accepts the canonical labels case-insensitively with
// surrounding whitespace trimmed. The error wraps the original input so
// callers can surface it verbatim to operators.
func ParseBudgetTier(s string) (BudgetTier, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "cheap":
		return BudgetCheap, nil
	case "balanced":
		return BudgetBalanced, nil
	case "premium":
		return BudgetPremium, nil
	default:
		return 0, fmt.Errorf("llm: invalid budget tier %q", s)
	}
}
