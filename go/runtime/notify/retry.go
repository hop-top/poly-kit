package notify

import (
	"context"
	"errors"
	"time"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/runtime/bus"
)

// RetrySink wraps an inner Sink, retrying Drain on error up to a
// configured maximum. Between attempts it sleeps for the duration
// returned by BackoffFunc, waking early if the context is canceled
// (RetrySink owns the timer/select around the sleep so a canceled
// ctx never blocks longer than necessary and never leaks a
// goroutine).
//
// On exhaustion, RetrySink either:
//   - routes the event to a configured dead-letter Sink (if
//     WithDeadLetter is set), or
//   - returns the last inner error (when no dead-letter is configured).
//
// # Open-circuit is terminal
//
// Per spec §3 decision #11, an error matching
// errors.Is(err, breaker.ErrBrokenCircuit) is the breaker's signal
// that egress is currently degraded. RetrySink treats it as terminal
// and short-circuits straight to the dead-letter sink (or returns the
// error unwrapped) without further retry attempts. Retrying would
// defeat the breaker.
//
// # Attempt indexing
//
// RetrySink's loop counter `attempt` is 0-indexed: attempt=0 is the
// initial try (no preceding sleep), attempt=1 is the first retry,
// etc. BackoffFunc itself is also 0-indexed (per backoff.go's godoc):
// the first retry calls backoff(0), the second retry calls
// backoff(1), and so on. RetrySink calls `backoff(attempt - 1)` to
// keep the BackoffFunc invariant intact.
//
// # Error wrapping
//
// RetrySink does not wrap errors. The last inner error (or the
// open-circuit error) is returned unwrapped, keeping
// errors.Is/errors.As behavior identical to calling the inner Sink
// directly. RetrySink is transparent in the error chain.
//
// Spec: docs/specs/notifications.md §3 #5, #6, #11; §7.
type RetrySink struct {
	inner bus.Sink
	opts  retryOpts
}

type retryOpts struct {
	maxAttempts int
	backoff     BackoffFunc
	deadLetter  bus.Sink
}

// RetryOption configures a RetrySink.
type RetryOption func(*retryOpts)

// WithMaxAttempts sets the total number of attempts (initial try
// plus retries). n=1 means "one attempt, no retries, no backoff".
// Values < 1 are clamped to 1 defensively — a 0-attempt config is
// almost certainly a bug, and silently dropping the event by never
// calling Drain would surprise callers. Default: 3.
func WithMaxAttempts(n int) RetryOption {
	return func(o *retryOpts) {
		o.maxAttempts = n
	}
}

// WithBackoff sets the backoff function consulted between retry
// attempts. Default: ExponentialBackoff(100*time.Millisecond, 2.0,
// true).
func WithBackoff(b BackoffFunc) RetryOption {
	return func(o *retryOpts) {
		if b != nil {
			o.backoff = b
		}
	}
}

// WithDeadLetter routes events that exhausted retries (or that hit a
// terminal open-circuit error) to dl. The dead-letter sink receives
// the original event with the original context.
//
// If dl.Drain returns an error, RetrySink.Drain returns that error.
// If dl is nil (the default), RetrySink.Drain returns the last inner
// error.
//
// Wiring the same sink as both inner and dead-letter is permitted
// but probably a bug; RetrySink.Close will close it twice in that
// case. Callers are responsible for the dedup if it matters to them.
func WithDeadLetter(dl bus.Sink) RetryOption {
	return func(o *retryOpts) {
		o.deadLetter = dl
	}
}

// NewRetrySink wraps inner with retry semantics. Defaults:
// maxAttempts=3, backoff=ExponentialBackoff(100ms, 2.0, true),
// no dead-letter.
func NewRetrySink(inner bus.Sink, opts ...RetryOption) *RetrySink {
	o := retryOpts{
		maxAttempts: 3,
		backoff:     ExponentialBackoff(100*time.Millisecond, 2.0, true),
	}
	for _, opt := range opts {
		opt(&o)
	}
	if o.maxAttempts < 1 {
		o.maxAttempts = 1
	}
	return &RetrySink{inner: inner, opts: o}
}

// Drain attempts inner.Drain up to maxAttempts times. On the second
// and later attempts it sleeps for the BackoffFunc-returned duration
// inside a timer/select, waking early on ctx cancellation. An inner
// error matching breaker.ErrBrokenCircuit short-circuits to the
// dead-letter / last-error path without further retries.
func (r *RetrySink) Drain(ctx context.Context, e bus.Event) error {
	var lastErr error
	for attempt := 0; attempt < r.opts.maxAttempts; attempt++ {
		if attempt > 0 {
			// First retry (attempt=1) consults backoff(0); second
			// retry consults backoff(1); etc.
			d := r.opts.backoff(attempt - 1)
			if d > 0 {
				t := time.NewTimer(d)
				select {
				case <-t.C:
				case <-ctx.Done():
					t.Stop()
					return ctx.Err()
				}
			}
			// Defensive: if d == 0 the timer arm above is skipped.
			// A ctx that was canceled while we were doing other
			// work should still fail fast before we hand a canceled
			// ctx to the inner sink.
			if err := ctx.Err(); err != nil {
				return err
			}
		}

		err := r.inner.Drain(ctx, e)
		if err == nil {
			return nil
		}
		lastErr = err

		// Open-circuit is terminal; do not retry.
		if errors.Is(err, breaker.ErrBrokenCircuit) {
			return r.dispatchDeadLetter(ctx, e, err)
		}
	}

	return r.dispatchDeadLetter(ctx, e, lastErr)
}

// dispatchDeadLetter routes the event to the dead-letter sink if
// configured, otherwise returns lastErr unwrapped.
func (r *RetrySink) dispatchDeadLetter(ctx context.Context, e bus.Event, lastErr error) error {
	if r.opts.deadLetter == nil {
		return lastErr
	}
	return r.opts.deadLetter.Drain(ctx, e)
}

// Close closes the inner sink, then the dead-letter sink (if set).
// The first error encountered is returned, but Close always attempts
// both regardless of inner.Close's outcome.
func (r *RetrySink) Close() error {
	var firstErr error
	if err := r.inner.Close(); err != nil {
		firstErr = err
	}
	if r.opts.deadLetter != nil {
		if err := r.opts.deadLetter.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// compile-time interface assertion.
var _ bus.Sink = (*RetrySink)(nil)
