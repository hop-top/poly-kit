// Provider picker: filters [aim.Registry] results by a [RequestProfile] and
// ranks survivors by [BudgetTier]. Token-window filters use a "no info, give
// benefit of doubt" rule so models with unknown Limit fields aren't dropped
// silently — picker honors only positively-known mismatches.

package llm

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"hop.top/aim"
)

// EliminationReason explains why a candidate was dropped during picking.
// Stage is one of the package-level Elim* constants.
type EliminationReason struct {
	Model  *aim.Model
	Stage  string
	Detail string
}

// Elimination stage labels. ElimPoolDisabled is reserved for a future task
// that wires pool config; the picker does not emit it today.
const (
	ElimContextWindow = "context_window"
	ElimOutputTokens  = "output_tokens"
	ElimPoolDisabled  = "pool_disabled"
)

// NoMatchError carries structured detail about why no provider qualified.
// CandidateCount is the post-Filter count returned by the registry, before
// token-window filtering — operators read it to distinguish "filter too
// narrow" from "all eliminated by budget caps".
type NoMatchError struct {
	Filter         aim.Filter
	CandidateCount int
	Eliminated     []EliminationReason
}

func (e *NoMatchError) Error() string {
	return fmt.Sprintf("llm: no provider matches: %d candidates, %d eliminated", e.CandidateCount, len(e.Eliminated))
}

// Is supports errors.Is(err, ErrNoProviderMatches) while keeping the
// structured detail addressable via errors.As.
func (e *NoMatchError) Is(target error) bool {
	return target == ErrNoProviderMatches
}

// ErrNoProviderMatches is the sentinel for "picker found no qualifying model".
// The wrapped value is always a *NoMatchError; use errors.As to inspect.
var ErrNoProviderMatches = errors.New("no provider matches")

// PickProvider selects the best aim.Model matching the profile under the
// budget tier. It queries reg.Models(ctx, profile.Filter), applies token-
// window filters from profile.MaxInputTokens / profile.MaxOutputTokens, then
// ranks the survivors by tier. Returns ErrNoProviderMatches (wrapping a
// *NoMatchError) when nothing qualifies.
//
// PickProvider is deterministic: ties are broken alphabetically by
// (Provider, ID).
func PickProvider(ctx context.Context, reg *aim.Registry, profile RequestProfile, budget BudgetTier) (*aim.Model, error) {
	models, err := reg.Models(ctx, profile.Filter)
	if err != nil {
		return nil, fmt.Errorf("llm: query registry: %w", err)
	}

	// Copy to a pointer slice so callers can hold a stable reference to the
	// winner without re-querying.
	candidates := make([]*aim.Model, 0, len(models))
	for i := range models {
		m := models[i]
		candidates = append(candidates, &m)
	}

	survivors, eliminated := applyTokenWindow(candidates, profile)
	if len(survivors) == 0 {
		nme := &NoMatchError{
			Filter:         profile.Filter,
			CandidateCount: len(candidates),
			Eliminated:     eliminated,
		}
		return nil, nme
	}

	winner := rank(survivors, budget)
	return winner, nil
}

// applyTokenWindow drops candidates whose known token limits violate the
// profile's bounds. Unknown limits (Context == 0 or Output == 0) pass — we
// only eliminate on positively-known mismatch.
func applyTokenWindow(candidates []*aim.Model, p RequestProfile) (survivors []*aim.Model, eliminated []EliminationReason) {
	for _, m := range candidates {
		if p.MaxInputTokens > 0 && m.Limit.Context > 0 && m.Limit.Context < p.MaxInputTokens {
			eliminated = append(eliminated, EliminationReason{
				Model:  m,
				Stage:  ElimContextWindow,
				Detail: fmt.Sprintf("context_window %d < required %d", m.Limit.Context, p.MaxInputTokens),
			})
			continue
		}
		if p.MaxOutputTokens > 0 && m.Limit.Output > 0 && m.Limit.Output < p.MaxOutputTokens {
			eliminated = append(eliminated, EliminationReason{
				Model:  m,
				Stage:  ElimOutputTokens,
				Detail: fmt.Sprintf("output_tokens %d < required %d", m.Limit.Output, p.MaxOutputTokens),
			})
			continue
		}
		survivors = append(survivors, m)
	}
	return survivors, eliminated
}

// weightedPrice computes the token-weighted USD price used by the Cheap and
// Balanced rankers. Nil Cost is treated as 0 so local/open-weight models
// preferred by Cheap aren't penalized for missing metadata.
func weightedPrice(m *aim.Model) float64 {
	if m.Cost == nil {
		return 0
	}
	return (m.Cost.Input * 0.75) + (m.Cost.Output * 0.25)
}

// inputCost returns Cost.Input or 0 when Cost is nil. Used as Premium's
// tiebreak — priced models beat nil-cost models for "more expensive ≈ more
// capable".
func inputCost(m *aim.Model) float64 {
	if m.Cost == nil {
		return 0
	}
	return m.Cost.Input
}

// rank applies the tier-specific ordering and returns the winner. All sorts
// are stable and end with the (Provider, ID) tiebreak so identical primary
// keys yield deterministic output across runs.
func rank(survivors []*aim.Model, budget BudgetTier) *aim.Model {
	switch budget {
	case BudgetPremium:
		sort.SliceStable(survivors, func(i, j int) bool {
			a, b := survivors[i], survivors[j]
			if a.Limit.Context != b.Limit.Context {
				return a.Limit.Context > b.Limit.Context
			}
			if inputCost(a) != inputCost(b) {
				return inputCost(a) > inputCost(b)
			}
			return providerIDLess(a, b)
		})
		return survivors[0]

	case BudgetBalanced:
		sort.SliceStable(survivors, func(i, j int) bool {
			a, b := survivors[i], survivors[j]
			pa, pb := weightedPrice(a), weightedPrice(b)
			if pa != pb {
				return pa < pb
			}
			return providerIDLess(a, b)
		})
		// Median index — for one survivor this is 0, for [a,b,c] this is b.
		return survivors[len(survivors)/2]

	default: // BudgetCheap
		sort.SliceStable(survivors, func(i, j int) bool {
			a, b := survivors[i], survivors[j]
			pa, pb := weightedPrice(a), weightedPrice(b)
			if pa != pb {
				return pa < pb
			}
			return providerIDLess(a, b)
		})
		return survivors[0]
	}
}

// providerIDLess orders models alphabetically by (Provider, ID) — the final
// deterministic tiebreak.
func providerIDLess(a, b *aim.Model) bool {
	if a.Provider != b.Provider {
		return a.Provider < b.Provider
	}
	return a.ID < b.ID
}
