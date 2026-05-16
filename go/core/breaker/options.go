package breaker

import (
	"context"
	"time"

	"charm.land/log/v2"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/bulkhead"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/fallback"
	"github.com/failsafe-go/failsafe-go/ratelimiter"
	"github.com/failsafe-go/failsafe-go/timeout"

	bpolicy "hop.top/kit/go/core/breaker/policy"
	"hop.top/kit/go/runtime/domain"
)

// Option configures a Breaker at construction time.
type Option func(*config)

// CircuitOpts tunes the underlying circuit breaker. All zero fields
// inherit defaults (FailureThreshold=5, SuccessThreshold=1,
// Delay=30s).
type CircuitOpts struct {
	FailureThreshold uint
	SuccessThreshold uint
	Delay            time.Duration
}

// FallbackFn is the user-supplied fallback invoked when the breaker
// degrades. Receives the executor's context.
type FallbackFn func(ctx context.Context) error

// config is the internal accumulator filled by Option callbacks. It
// is the single source of truth for what policies the executor will
// be composed from, in what order. Composition order (innermost
// first): Volume/Count → RateLimiter → Bulkhead → CircuitBreaker →
// Fallback.
type config struct {
	action Action

	circFailureThreshold uint
	circSuccessThreshold uint
	circDelay            time.Duration

	rateLimit       *rateLimitOpts
	maxConcurrent   uint
	hasBulkhead     bool
	timeoutDuration time.Duration
	hasTimeout      bool

	maxBytes int64
	hasBytes bool
	maxOps   int64
	hasOps   bool

	fallback FallbackFn

	logger *log.Logger

	publisher domain.EventPublisher
	topics    Topics
}

type rateLimitOpts struct {
	max    uint
	period time.Duration
}

func newConfig(opts ...Option) *config {
	c := &config{
		action:               Halt,
		circFailureThreshold: 5,
		circSuccessThreshold: 1,
		circDelay:            30 * time.Second,
		topics:               DefaultTopics,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// buildCircuitBreaker constructs the base circuit policy. We always
// build one so callers get a real state machine + listeners even
// without explicit tuning. Listeners close over b so their log
// events carry the breaker name + live trip count.
func buildCircuitBreaker(b *breakerImpl, c *config) circuitbreaker.CircuitBreaker[any] {
	bldr := circuitbreaker.NewBuilder[any]().
		WithFailureThreshold(c.circFailureThreshold).
		WithSuccessThreshold(c.circSuccessThreshold).
		WithDelay(c.circDelay)
	bldr = installCircuitListeners(bldr, b)
	return bldr.Build()
}

// assemblePolicies returns the policy slice in failsafe.With order:
// OUTERMOST first. failsafe.With(p1, p2, p3) composes as
// p1(p2(p3(fn))), so we list Fallback first (so it can catch all
// inner failures), then CircuitBreaker (observes everything inner),
// then Bulkhead, then RateLimiter, then Volume/Count last (cheapest
// pre-flight, runs first against fn).
func assemblePolicies(b *breakerImpl, cb circuitbreaker.CircuitBreaker[any], c *config) []failsafe.Policy[any] {
	out := []failsafe.Policy[any]{}

	if c.fallback != nil {
		fb := c.fallback
		out = append(out, fallback.NewBuilderWithFunc[any](
			func(_ failsafe.Execution[any]) (any, error) {
				return nil, fb(context.Background())
			},
		).Build())
	}

	out = append(out, cb)

	if c.hasTimeout {
		out = append(out, timeout.NewBuilder[any](c.timeoutDuration).Build())
	}
	if c.hasBulkhead {
		out = append(out, bulkhead.NewBuilder[any](c.maxConcurrent).Build())
	}
	if c.rateLimit != nil {
		// Bursty (period-windowed) rather than Smooth (equally-spaced):
		// users asking for "100 per minute" expect to be able to fire
		// 100 within the first second, not be paced to one per 600ms.
		out = append(out, ratelimiter.NewBurstyBuilder[any](
			c.rateLimit.max, c.rateLimit.period,
		).Build())
	}
	if c.hasBytes {
		out = append(out, bpolicy.NewVolume[any]().
			WithMaxBytes(c.maxBytes).
			WithReader(func() int64 { return b.counterValue("bytes") }).
			Build())
	}
	if c.hasOps {
		out = append(out, bpolicy.NewCount[any]().
			WithMaxOps(c.maxOps).
			WithReader(func() int64 { return b.counterValue("ops") }).
			Build())
	}

	return out
}

// OnTrip selects the action taken when the circuit opens. Halt is
// the default (fail closed) and is recommended; Degrade and Warn
// exist for migration / known soft-failure modes.
func OnTrip(a Action) Option {
	return func(c *config) { c.action = a }
}

// MaxPerInterval limits executions to n per period via failsafe's
// smooth (token-bucket) rate limiter.
func MaxPerInterval(n int, d time.Duration) Option {
	return func(c *config) {
		c.rateLimit = &rateLimitOpts{max: uint(n), period: d}
	}
}

// MaxPerMinute is shorthand for MaxPerInterval(n, time.Minute).
func MaxPerMinute(n int) Option {
	return MaxPerInterval(n, time.Minute)
}

// MaxConcurrent caps in-flight executions via failsafe's bulkhead.
func MaxConcurrent(n int) Option {
	return func(c *config) {
		c.maxConcurrent = uint(n)
		c.hasBulkhead = true
	}
}

// Timeout adds a per-execution timeout policy. Only meaningful for
// callers that drop down to b.Executor().Run/Get with real work;
// Allow uses an instant noop fn so the timeout never fires there.
func Timeout(d time.Duration) Option {
	return func(c *config) {
		c.timeoutDuration = d
		c.hasTimeout = true
	}
}

// MaxBytes installs a Volume policy that trips Allow once the
// cumulative bytes recorded via Record(_, n) reach n.
func MaxBytes(n int64) Option {
	return func(c *config) {
		c.maxBytes = n
		c.hasBytes = true
	}
}

// MaxOps installs a Count policy that trips Allow once the
// cumulative number of Record calls reaches n.
func MaxOps(n int64) Option {
	return func(c *config) {
		c.maxOps = n
		c.hasOps = true
	}
}

// WithCircuit overrides the default circuit tuning. Zero fields keep
// the default (FailureThreshold=5, SuccessThreshold=1, Delay=30s).
func WithCircuit(opts CircuitOpts) Option {
	return func(c *config) {
		if opts.FailureThreshold > 0 {
			c.circFailureThreshold = opts.FailureThreshold
		}
		if opts.SuccessThreshold > 0 {
			c.circSuccessThreshold = opts.SuccessThreshold
		}
		if opts.Delay > 0 {
			c.circDelay = opts.Delay
		}
	}
}

// ResetAfter is a convenience for WithCircuit{Delay: d} — the time
// the breaker waits in Open before transitioning to HalfOpen.
func ResetAfter(d time.Duration) Option {
	return func(c *config) { c.circDelay = d }
}

// Fallback attaches a function to run when the breaker degrades.
// Pair with OnTrip(Degrade); without a Fallback, Degrade silently
// behaves like Halt.
func Fallback(fn FallbackFn) Option {
	return func(c *config) { c.fallback = fn }
}

// Logger overrides the structured logger used for trip events. When
// unset, kit/breaker resolves a *log.Logger via kitlog.New(viper.GetViper())
// at use time so adopter --quiet / --no-color settings flow through.
//
// BREAKING (ADR-0007): the parameter type was *slog.Logger; it is now
// charm.land/log/v2.*Logger. Adopters that constructed a slog logger
// must build a charm/log logger instead (see kit/console/log).
func Logger(l *log.Logger) Option {
	return func(c *config) { c.logger = l }
}
