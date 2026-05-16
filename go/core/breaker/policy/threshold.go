// Package policy implements the failsafe policies kit/breaker adds
// on top of failsafe-go: Volume (cumulative bytes) and Count
// (cumulative ops). Both compose as failsafe.Policy[R] so they live
// alongside RateLimiter, Bulkhead, CircuitBreaker, etc. in the same
// Executor.
//
// The kit-side Breaker maintains its own counter map keyed by
// well-known names ("bytes", "ops"). Each policy reads the current
// value via a Reader closure passed at Build time. This avoids
// touching failsafe internals and keeps the policy goroutine-safe
// when the underlying counter source is.
package policy

import (
	"errors"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/common"
	"github.com/failsafe-go/failsafe-go/policy"
)

// ErrThresholdExceeded is returned (via failsafe's executor result)
// when a Volume or Count policy's cumulative counter has reached or
// exceeded its configured cap. kit/breaker.Allow maps this to its
// single sentinel ErrBrokenCircuit, but consumers wiring policies
// directly into a failsafe Executor see this error.
var ErrThresholdExceeded = errors.New("breaker/policy: threshold exceeded")

// Reader returns the current cumulative counter value the policy
// should compare against its limit. Callers usually back this with
// an atomic.Int64.Load.
type Reader func() int64

// threshold is the shared implementation behind Volume and Count.
// Both are int64 cumulative comparisons against a max; the only
// difference is the well-known counter name and the Builder ergonomics.
type threshold[R any] struct {
	max        int64
	read       Reader
	onExceeded func(int64)
}

// thresholdExecutor wraps a threshold as a failsafe policy.Executor.
type thresholdExecutor[R any] struct {
	policy.BaseExecutor[R]
	*threshold[R]
}

// Marker so failsafe accepts this as result-agnostic — Volume/Count
// don't transform the inner result.
func (t *threshold[R]) ResultAgnostic() {}

// ToExecutor wires the policy into failsafe's pipeline.
func (t *threshold[R]) ToExecutor(_ R) any {
	te := &thresholdExecutor[R]{threshold: t}
	te.Executor = te
	return te
}

// PreExecute fires before the inner fn. If the cumulative counter is
// already at or above the cap, short-circuit with ErrThresholdExceeded
// and trigger the OnExceeded hook.
func (te *thresholdExecutor[R]) PreExecute(_ policy.ExecutionInternal[R]) *common.PolicyResult[R] {
	current := te.read()
	if current >= te.max {
		if te.onExceeded != nil {
			te.onExceeded(current)
		}
		return &common.PolicyResult[R]{
			Error: ErrThresholdExceeded,
			Done:  true,
		}
	}
	return nil
}

// Statically assert that thresholdExecutor satisfies the failsafe
// executor contract — catches breakage on failsafe upgrades.
var _ policy.Executor[any] = (*thresholdExecutor[any])(nil)

// build validates required fields and returns the threshold ready
// for use in a failsafe Executor. Both Reader and a non-zero max are
// required — silently accepting either zero would let the policy be
// silently no-op, which is worse than panicking at construction.
func build[R any](max int64, read Reader, onExceeded func(int64)) failsafe.Policy[R] {
	if max <= 0 {
		panic("breaker/policy: max must be > 0")
	}
	if read == nil {
		panic("breaker/policy: reader is required")
	}
	return &threshold[R]{
		max:        max,
		read:       read,
		onExceeded: onExceeded,
	}
}
