// Provider picker: filters [aim.Registry] results by a [RequestProfile] and
// ranks survivors by [BudgetTier]. Token-window filters use a "no info, give
// benefit of doubt" rule so models with unknown Limit fields aren't dropped
// silently — picker honors only positively-known mismatches.
//
// Tracing
//
// PickProvider emits one structured slog event per call when the
// LLM_PICKER_TRACE environment variable is set to a recognised truthy value
// ("1", "true", "on", "yes"; case-insensitive). Anything else, including
// unset, suppresses the event. The event is emitted via
// [slog.InfoContext] on [slog.Default] just before the call returns
// successfully or with [ErrNoProviderMatches]; other errors (e.g. registry
// query failures) propagate without an extra trace line.
//
// Stable attribute keys:
//
//   - picker.budget — budget tier label ("cheap" / "balanced" / "premium").
//   - picker.filter.tool_call — "true" / "false" / "<nil>".
//   - picker.filter.reasoning — same tristate encoding.
//   - picker.filter.structured_output — same tristate encoding.
//   - picker.filter.temperature — same tristate encoding.
//   - picker.filter.provider — Filter.Provider; omitted when empty.
//   - picker.filter.family — Filter.Family; omitted when empty.
//   - picker.filter.input — comma-joined input modalities; omitted when empty.
//   - picker.filter.output — comma-joined output modalities; omitted when empty.
//   - picker.profile.max_input_tokens — MaxInputTokens; omitted when 0.
//   - picker.profile.max_output_tokens — MaxOutputTokens; omitted when 0.
//   - picker.candidate_count — models returned by [aim.Registry.Models] before
//     token-window filtering.
//   - picker.eliminated_count — total token-window eliminations.
//   - picker.outcome — "matched" or "no_match".
//   - picker.chosen.provider / picker.chosen.model — present only on matched.
//
// Sample log line:
//
//	level=INFO msg=llm.pick picker.budget=balanced picker.filter.tool_call=true picker.filter.reasoning=<nil> picker.filter.structured_output=<nil> picker.filter.temperature=<nil> picker.profile.max_input_tokens=8192 picker.candidate_count=12 picker.eliminated_count=3 picker.outcome=matched picker.chosen.provider=openai picker.chosen.model=gpt-4o

package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

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
	trace := tracingEnabled()
	if len(survivors) == 0 {
		nme := &NoMatchError{
			Filter:         profile.Filter,
			CandidateCount: len(candidates),
			Eliminated:     eliminated,
		}
		if trace {
			emitPickTrace(ctx, profile, budget, len(candidates), len(eliminated), nil)
		}
		return nil, nme
	}

	winner := rank(survivors, budget)
	if trace {
		emitPickTrace(ctx, profile, budget, len(candidates), len(eliminated), winner)
	}
	return winner, nil
}

// emitPickTrace logs one llm.pick event. winner == nil means no-match;
// otherwise the event records the chosen provider/model.
func emitPickTrace(ctx context.Context, profile RequestProfile, budget BudgetTier, candidateCount, eliminatedCount int, winner *aim.Model) {
	attrs := []slog.Attr{
		slog.String("picker.budget", budget.String()),
		slog.String("picker.filter.tool_call", tristateString(profile.Filter.ToolCall)),
		slog.String("picker.filter.reasoning", tristateString(profile.Filter.Reasoning)),
		slog.String("picker.filter.structured_output", tristateString(profile.Filter.StructuredOutput)),
		slog.String("picker.filter.temperature", tristateString(profile.Filter.Temperature)),
	}
	if profile.Filter.Provider != "" {
		attrs = append(attrs, slog.String("picker.filter.provider", profile.Filter.Provider))
	}
	if profile.Filter.Family != "" {
		attrs = append(attrs, slog.String("picker.filter.family", profile.Filter.Family))
	}
	if len(profile.Filter.Input) > 0 {
		attrs = append(attrs, slog.String("picker.filter.input", strings.Join(profile.Filter.Input, ",")))
	}
	if len(profile.Filter.Output) > 0 {
		attrs = append(attrs, slog.String("picker.filter.output", strings.Join(profile.Filter.Output, ",")))
	}
	if profile.MaxInputTokens > 0 {
		attrs = append(attrs, slog.Int("picker.profile.max_input_tokens", profile.MaxInputTokens))
	}
	if profile.MaxOutputTokens > 0 {
		attrs = append(attrs, slog.Int("picker.profile.max_output_tokens", profile.MaxOutputTokens))
	}
	attrs = append(
		attrs,
		slog.Int("picker.candidate_count", candidateCount),
		slog.Int("picker.eliminated_count", eliminatedCount),
	)
	if winner == nil {
		attrs = append(attrs, slog.String("picker.outcome", "no_match"))
	} else {
		attrs = append(
			attrs,
			slog.String("picker.outcome", "matched"),
			slog.String("picker.chosen.provider", winner.Provider),
			slog.String("picker.chosen.model", winner.ID),
		)
	}
	slog.Default().LogAttrs(ctx, slog.LevelInfo, "llm.pick", attrs...)
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
