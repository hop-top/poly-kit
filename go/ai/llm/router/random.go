package router

import (
	"context"
	"math/rand"
)

// RandomRouter returns a uniform random score in [0,1].
// Useful for baseline comparisons and testing.
type RandomRouter struct {
	rng *rand.Rand
}

// NewRandomRouter creates a RandomRouter. If rng is nil, the global
// math/rand source is used.
func NewRandomRouter(rng *rand.Rand) *RandomRouter {
	return &RandomRouter{rng: rng}
}

// Score returns a uniform random float in [0,1].
func (r *RandomRouter) Score(_ context.Context, _ string) (float64, error) {
	if r.rng != nil {
		return r.rng.Float64(), nil
	}
	return rand.Float64(), nil
}
