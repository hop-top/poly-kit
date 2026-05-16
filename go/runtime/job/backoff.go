package job

import (
	"math"
	"math/rand/v2"
	"time"
)

// BackoffStrategy defines exponential backoff parameters for job retries.
type BackoffStrategy struct {
	Initial time.Duration `json:"initial"` // default 30s
	Max     time.Duration `json:"max"`     // default 15m
	Factor  float64       `json:"factor"`  // default 2.0
	Jitter  float64       `json:"jitter"`  // default 0.25
}

// DefaultBackoff returns the default backoff strategy.
func DefaultBackoff() BackoffStrategy {
	return BackoffStrategy{
		Initial: 30 * time.Second,
		Max:     15 * time.Minute,
		Factor:  2.0,
		Jitter:  0.25,
	}
}

// withDefaults returns a copy with zero fields replaced by defaults.
// Jitter is excluded: zero means no jitter (deterministic).
func (b BackoffStrategy) withDefaults() BackoffStrategy {
	d := DefaultBackoff()
	if b.Initial == 0 {
		b.Initial = d.Initial
	}
	if b.Max == 0 {
		b.Max = d.Max
	}
	if b.Factor == 0 {
		b.Factor = d.Factor
	}
	// Jitter=0 is valid (no jitter); only default when using DefaultBackoff().
	return b
}

// Compute returns the backoff duration for the given attempt number
// (0-indexed). The result grows exponentially with jitter, capped at Max.
func (b BackoffStrategy) Compute(attempts int) time.Duration {
	b = b.withDefaults()

	base := float64(b.Initial) * math.Pow(b.Factor, float64(attempts))
	if base > float64(b.Max) {
		base = float64(b.Max)
	}

	// Apply jitter: ±jitter fraction of base.
	jitterRange := base * b.Jitter
	jittered := base + jitterRange*(2*rand.Float64()-1)

	if jittered < 0 {
		jittered = 0
	}
	if jittered > float64(b.Max) {
		jittered = float64(b.Max)
	}

	return time.Duration(jittered)
}
