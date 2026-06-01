// Pool-aware wrapper around PickProvider. Lives in its own file so the pool
// integration stays additive: picker.go owns the budget-ranking pipeline,
// this file owns the pre-filter that narrows the registry's candidate set
// to the operator-approved pool.

package llm

import (
	"context"
	"fmt"

	"hop.top/aim"
)

// PickProviderInPool wraps PickProvider with a pool pre-filter. The pool
// restricts which (scheme, model) candidates from the registry qualify.
// Equivalent to PickProvider when pool is empty.
//
// Eliminations from the pool filter are merged into NoMatchError.Eliminated
// alongside the usual context_window / output_tokens reasons so operators
// can distinguish "pool too narrow" from "all pool members eliminated by
// budget caps".
func PickProviderInPool(ctx context.Context, reg *aim.Registry, profile RequestProfile, budget BudgetTier, pool []PoolEntry) (*aim.Model, error) {
	models, err := reg.Models(ctx, profile.Filter)
	if err != nil {
		return nil, fmt.Errorf("llm: query registry: %w", err)
	}

	// Pool first so eliminations include disabled-by-pool reasons even when
	// the survivors would otherwise pass the token-window stage.
	poolSurvivors, poolEliminated := FilterByPool(models, pool)

	candidates := make([]*aim.Model, 0, len(poolSurvivors))
	for i := range poolSurvivors {
		m := poolSurvivors[i]
		candidates = append(candidates, &m)
	}

	survivors, tokenEliminated := applyTokenWindow(candidates, profile)
	trace := tracingEnabled()
	if len(survivors) == 0 {
		nme := &NoMatchError{
			Filter:         profile.Filter,
			CandidateCount: len(models),
			Eliminated:     append(poolEliminated, tokenEliminated...),
		}
		if trace {
			emitPickTrace(ctx, profile, budget, len(models), len(poolEliminated)+len(tokenEliminated), nil)
		}
		return nil, nme
	}

	winner := rank(survivors, budget)
	if trace {
		emitPickTrace(ctx, profile, budget, len(models), len(poolEliminated)+len(tokenEliminated), winner)
	}
	return winner, nil
}
