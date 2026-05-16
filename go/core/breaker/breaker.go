package breaker

import (
	"sync"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
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

// State is the lifecycle of a breaker. 1:1 with failsafe's
// circuitbreaker.State.
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

// breakerImpl is the concrete Breaker. The failsafe executor + cb
// give us the state machine and listeners; mu protects stats and
// the kit-side Volume/Count counters.
type breakerImpl struct {
	name string

	cfg      *config
	executor failsafe.Executor[any]
	cb       circuitbreaker.CircuitBreaker[any]

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
	// Build the cb after b exists so its listeners can close over
	// b's logger + tripsSnapshot. Then assemble policies (Volume/
	// Count closures also need b for counterValue access).
	b.cb = buildCircuitBreaker(b, cfg)
	b.executor = failsafe.With(assemblePolicies(b, b.cb, cfg)...)

	registerOrPanic(name, b)
	return b
}

// Allow runs the executor's no-op and maps any failure to the single
// ErrBrokenCircuit sentinel.
func (b *breakerImpl) Allow() error {
	if err := b.executor.Run(noopFn); err != nil {
		return ErrBrokenCircuit
	}
	return nil
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

	if success {
		b.cb.RecordSuccess()
		return
	}
	b.cb.RecordFailure()
}

func (b *breakerImpl) State() State {
	return mapState(b.cb.State())
}

func (b *breakerImpl) Reset() {
	b.mu.Lock()
	b.counters = map[string]int64{}
	b.stats.Counters = map[string]int64{}
	b.mu.Unlock()

	b.logReset()
	b.cb.Close()
}

func (b *breakerImpl) Trip(reason string) {
	b.mu.Lock()
	b.stats.Trips++
	b.stats.LastTripAt = time.Now()
	b.stats.LastTripReason = reason
	b.mu.Unlock()

	b.logTrip(reason)
	b.cb.Open()
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

// noopFn is the function we hand to the executor for Allow checks —
// it succeeds; the policies decide whether to short-circuit.
func noopFn() error { return nil }

// mapState converts the failsafe state enum to the kit/breaker enum.
func mapState(s circuitbreaker.State) State {
	switch s {
	case circuitbreaker.OpenState:
		return Open
	case circuitbreaker.HalfOpenState:
		return HalfOpen
	default:
		return Closed
	}
}
