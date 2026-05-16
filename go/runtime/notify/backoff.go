package notify

import (
	"math"
	"math/rand"
	"time"
)

// BackoffFunc returns the duration to sleep before the given attempt.
// attempt is 0-indexed: attempt=0 is the first retry (after the
// initial failure), attempt=1 is the second retry, etc.
//
// BackoffFunc is intentionally PURE: it takes only an attempt number
// and returns a duration. It does NOT accept a context and cannot
// wake early. Context cancellation is the responsibility of the
// caller (RetrySink in this package), which owns the timer/select
// around the sleep. See docs/specs/notifications.md §7 for the
// cancellation-ownership rationale.
type BackoffFunc func(attempt int) time.Duration

// ExponentialBackoff returns a BackoffFunc that grows the delay
// exponentially as base * factor^attempt. If jitter is true, the
// returned duration is uniformly randomized in
// [0, base*factor^attempt) to spread retries across callers.
//
// Edge cases:
//   - base <= 0 always returns 0 (defensive: a negative base must
//     not yield a negative time.Duration).
//   - factor == 1 returns base for every attempt (no growth).
//   - Very large attempts that would overflow float64 to +Inf, or
//     overflow time.Duration when converted from float64
//     nanoseconds, are clamped to time.Duration(math.MaxInt64). This
//     prevents the silent wrap-to-negative footgun in
//     time.Duration math.
//
// The jitter PRNG is rand.Float64() from math/rand. This is
// appropriate for retry spreading; do NOT rely on it for
// cryptographic unpredictability. The global math/rand source is
// seeded automatically by the runtime in Go 1.20+ (this module
// targets go 1.26.1 — see go.mod), so no manual seeding is needed.
func ExponentialBackoff(base time.Duration, factor float64, jitter bool) BackoffFunc {
	return func(attempt int) time.Duration {
		if base <= 0 {
			return 0
		}
		// base * factor^attempt, in float64 nanoseconds.
		d := float64(base) * math.Pow(factor, float64(attempt))
		// Guard against NaN and ±Inf (factor + large attempt can
		// blow up float64 to +Inf).
		if math.IsNaN(d) || math.IsInf(d, 0) || d <= 0 {
			return 0
		}
		if jitter {
			d = rand.Float64() * d
		}
		// Cap before converting to time.Duration: time.Duration is
		// int64 nanoseconds and silently wraps on overflow.
		if d >= float64(math.MaxInt64) {
			return time.Duration(math.MaxInt64)
		}
		return time.Duration(d)
	}
}
