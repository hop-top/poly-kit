package breaker

import (
	"context"
	"sync"
	"time"

	"github.com/failsafe-go/failsafe-go"
)

// Action is what the breaker does on trip. Halt is the safe default
// (fail closed). Degrade requires a Fallback to be configured. Warn
// turns Allow into a no-op + log only — for migration / known
// soft-failure modes only.
type Action int

const (
	// Halt fails closed: Allow returns ErrBrokenCircuit.
	Halt Action = iota

	// Degrade routes to a configured Fallback rather than failing.
	Degrade

	// Warn logs the trip but lets Allow return nil.
	Warn
)

// String returns the lowercase action name used in YAML configs and
// log attrs.
func (a Action) String() string {
	switch a {
	case Halt:
		return "halt"
	case Degrade:
		return "degrade"
	case Warn:
		return "warn"
	default:
		return "unknown"
	}
}

// State is the lifecycle of a breaker's circuit.
type State int

const (
	// Closed: executions are allowed.
	Closed State = iota

	// Open: executions short-circuit with ErrBrokenCircuit.
	Open

	// HalfOpen: probe state after the configured Delay; a few trial
	// executions decide whether to Close or re-Open.
	HalfOpen
)

// String returns the snake_case state name used in log attrs.
func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// Stats is a point-in-time snapshot of breaker activity.
type Stats struct {
	// Trips counts every open: manual Trip calls and automatic opens
	// (LastTripReason "circuit").
	Trips          uint64
	LastTripAt     time.Time
	LastTripReason string
	// Counters are lifetime cumulative counts keyed by well-known
	// names: "bytes" for Volume, "ops" for Count.
	Counters map[string]int64
}

// Breaker is the kit-flavored wrapper over a failsafe Executor.
// All methods are safe for concurrent use.
type Breaker interface {
	// Allow runs a no-op execution through the configured policies
	// and returns nil if allowed, ErrBrokenCircuit otherwise.
	Allow() error

	// Record feeds the cumulative Volume (n bytes) and Count (1 op)
	// counters used by the kit-side Volume/Count thresholds. Callers
	// that don't care about Volume/Count can ignore this.
	Record(success bool, n int64)

	// State returns the current State.
	State() State

	// Reset closes the circuit and zeroes the Volume/Count counters.
	Reset()

	// Trip manually opens the circuit. The reason is recorded in
	// Stats and emitted on the trip log event.
	Trip(reason string)

	// Stats returns a point-in-time snapshot.
	Stats() Stats

	// Name is the unique registry key.
	Name() string

	// Executor exposes the underlying failsafe executor for power
	// users who want to drop down to failsafe directly without
	// escaping the kit/breaker registry. Composes any additional
	// policies via failsafehttp etc.
	Executor() failsafe.Executor[any]
}

// breakerImpl is the concrete Breaker. circ is the kit-side circuit
// state machine; the failsafe executor composes it with the other
// policies. mu protects stats and the kit-side Volume/Count counters.
type breakerImpl struct {
	name string

	cfg      *config
	executor failsafe.Executor[any]
	// preflight runs Allow's no-op through the policies inside the
	// circuit (bulkhead, rate, volume, count); nil when none is set.
	preflight failsafe.Executor[any]
	circ      *circuit

	mu       sync.Mutex
	stats    Stats
	counters map[string]int64
}

// New constructs a Breaker, registers it under name, and returns it.
// Panics on empty name or double registration; tests should call
// Unregister via t.Cleanup.
func New(name string, opts ...Option) Breaker {
	if name == "" {
		panic("breaker: name is required")
	}

	cfg := newConfig(opts...)

	b := &breakerImpl{
		name:     name,
		cfg:      cfg,
		stats:    Stats{Counters: map[string]int64{}},
		counters: map[string]int64{},
	}
	b.circ = newCircuit(cfg.clock, cfg.circFailureThreshold, cfg.circSuccessThreshold, cfg.circDelay)
	// The circuit policy and the Volume/Count closures need b.
	outer, inner := assemblePolicies(b, &circuitPolicy{b: b}, cfg)
	b.executor = failsafe.With(append(outer, inner...)...)
	if len(inner) > 0 {
		b.preflight = failsafe.With(inner...)
	}

	registerOrPanic(name, b)
	return b
}

// Allow asks the circuit for a permit, then runs a no-op through the
// pre-flight policies. Neither outcome is recorded on the circuit:
// only Record (or a real execution through Executor) feeds it. A
// permit the circuit granted is returned when a pre-flight policy
// rejects.
func (b *breakerImpl) Allow() error {
	if !b.acquire() {
		return b.reject()
	}
	if b.preflight != nil {
		if err := b.preflight.Run(noopFn); err != nil {
			b.circ.release()
			return b.reject()
		}
	}
	return nil
}

// reject is Allow's answer once a policy refused: the fallback's
// verdict when one is configured, ErrBrokenCircuit otherwise.
func (b *breakerImpl) reject() error {
	if fb := b.cfg.fallback; fb != nil {
		if err := fb(context.Background()); err == nil {
			return nil
		}
	}
	return ErrBrokenCircuit
}

// Record updates the kit-side cumulative counters and forwards the
// success/failure outcome to the underlying circuit breaker so its
// state machine sees real signal.
func (b *breakerImpl) Record(success bool, n int64) {
	b.mu.Lock()
	b.counters["bytes"] += n
	b.counters["ops"]++
	// publish to the snapshot map too so Stats() copies are atomic
	b.stats.Counters["bytes"] = b.counters["bytes"]
	b.stats.Counters["ops"] = b.counters["ops"]
	b.mu.Unlock()

	b.recordCircuit(success)
}

func (b *breakerImpl) State() State {
	s, ts := b.circ.current()
	b.emitTransitions(ts)
	return s
}

// acquire asks the circuit to admit one execution and emits any
// transition that took.
func (b *breakerImpl) acquire() bool {
	ok, ts := b.circ.tryAcquire()
	b.emitTransitions(ts)
	return ok
}

// recordCircuit feeds one outcome to the circuit and emits any
// transition it caused.
func (b *breakerImpl) recordCircuit(success bool) {
	b.emitTransitions(b.circ.record(success))
}

func (b *breakerImpl) Reset() {
	b.mu.Lock()
	b.counters = map[string]int64{}
	b.stats.Counters = map[string]int64{}
	b.mu.Unlock()

	b.logReset()
	b.emitTransitions(b.circ.close())
}

func (b *breakerImpl) Trip(reason string) {
	b.countTrip(reason)
	b.logTrip(reason)
	b.emitTransitions(b.circ.open(true))
}

func (b *breakerImpl) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := Stats{
		Trips:          b.stats.Trips,
		LastTripAt:     b.stats.LastTripAt,
		LastTripReason: b.stats.LastTripReason,
		Counters:       make(map[string]int64, len(b.stats.Counters)),
	}
	for k, v := range b.stats.Counters {
		out.Counters[k] = v
	}
	return out
}

func (b *breakerImpl) Name() string                     { return b.name }
func (b *breakerImpl) Executor() failsafe.Executor[any] { return b.executor }

// counterValue returns the current value for a well-known counter
// name ("bytes", "ops"). Used by Volume/Count policy closures.
func (b *breakerImpl) counterValue(name string) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.counters[name]
}

// noopFn is the function Allow hands to the pre-flight executor; it
// succeeds, the policies decide whether to short-circuit.
func noopFn() error { return nil }
