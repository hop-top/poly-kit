// Pool-aware candidate filter — kept separate from picker.go so callers can
// compose pool gating with future filtering primitives (capability, region,
// quota) without forking the picker's ranking logic.

package llm

import (
	"hop.top/aim"
)

// FilterByPool keeps only those models in candidates whose (Scheme, Model)
// appears in pool with Enabled=true. Models eliminated by pool produce
// EliminationReason entries with Stage = "pool_disabled" so callers can
// surface them through NoMatchError.
//
// When pool is empty, FilterByPool returns candidates unchanged with no
// eliminations (no-pool means "accept everything from registry").
func FilterByPool(candidates []aim.Model, pool []PoolEntry) (survivors []aim.Model, eliminated []EliminationReason) {
	if len(pool) == 0 {
		return candidates, nil
	}

	// allow indexes by Provider+":"+ID; disabled entries are intentionally
	// absent so they fall through to the elimination branch.
	allow := make(map[string]struct{}, len(pool))
	for _, p := range pool {
		if !p.Enabled {
			continue
		}
		allow[p.Scheme+":"+p.Model] = struct{}{}
	}

	for i := range candidates {
		m := candidates[i]
		key := m.Provider + ":" + m.ID
		if _, ok := allow[key]; ok {
			survivors = append(survivors, m)
			continue
		}
		mc := m
		eliminated = append(eliminated, EliminationReason{
			Model:  &mc,
			Stage:  ElimPoolDisabled,
			Detail: "not in pool or disabled",
		})
	}
	return survivors, eliminated
}
