package breaker

import (
	"context"

	"charm.land/log/v2"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/spf13/viper"

	kitlog "hop.top/kit/go/console/log"
)

// eventSource is the source string passed alongside every breaker
// bus event. Per domain.EventPublisher it's a free-form dotted
// identifier; "core.breaker" identifies the originating package.
const eventSource = "core.breaker"

// logger returns the configured *log.Logger or a viper-backed kit/log
// default so listeners always have a destination. The default flows
// adopter --quiet / --no-color settings through (ADR-0007).
func (b *breakerImpl) logger() *log.Logger {
	if b.cfg != nil && b.cfg.logger != nil {
		return b.cfg.logger
	}
	return kitlog.New(viper.GetViper())
}

// logTrip is emitted from manual Trip(reason) before the underlying
// circuit's listener sees the transition. Always ERROR — manual
// trips are operator/audit events. Also publishes a TrippedPayload
// to the bus when a publisher is configured (best-effort, fire-and-
// forget; matches the post-transition pattern in
// runtime/domain.StateMachine).
func (b *breakerImpl) logTrip(reason string) {
	prev := b.State()
	b.logger().Error("breaker tripped manually",
		"breaker.name", b.name,
		"breaker.state", Open.String(),
		"breaker.reason", reason,
		"breaker.policy", "manual",
		"breaker.trips_total", b.tripsSnapshot(),
	)
	b.publishTripped(reason, prev, Open)
}

// publishTripped fires a TrippedPayload on the configured publisher.
// No-op when no publisher is configured. Fire-and-forget goroutine:
// the breaker must never block on subscriber cost.
func (b *breakerImpl) publishTripped(reason string, from, to State) {
	if b.cfg == nil || b.cfg.publisher == nil {
		return
	}
	pub := b.cfg.publisher
	topic := string(b.cfg.topics.Tripped)
	payload := TrippedPayload{Reason: reason, From: from, To: to}
	go func() {
		_ = pub.Publish(context.Background(), topic, eventSource, payload)
	}()
}

// publishTransition fires a TransitionPayload on the configured
// publisher for an automatic state transition. No-op when no
// publisher is configured. Fire-and-forget goroutine.
func (b *breakerImpl) publishTransition(prev, next State, trigger string) {
	if b.cfg == nil || b.cfg.publisher == nil {
		return
	}
	pub := b.cfg.publisher
	topic := string(b.cfg.topics.topicForState(next))
	payload := TransitionPayload{From: prev, To: next, Trigger: trigger}
	go func() {
		_ = pub.Publish(context.Background(), topic, eventSource, payload)
	}()
}

// logReset records a manual Reset call. INFO — the operator chose
// to recover the breaker.
func (b *breakerImpl) logReset() {
	b.logger().Info("breaker reset",
		"breaker.name", b.name,
		"breaker.state", Closed.String(),
		"breaker.policy", "manual",
	)
}

func (b *breakerImpl) tripsSnapshot() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stats.Trips
}

// transitionLevel maps a (prev,next) State pair to a charm/log level
// per the table above. Defaults to INFO for any unhandled combination.
func transitionLevel(prev, next State) log.Level {
	switch {
	case prev == Closed && next == Open:
		return log.ErrorLevel
	case prev == HalfOpen && next == Open:
		return log.WarnLevel
	case prev == Closed && next == HalfOpen:
		return log.WarnLevel
	case prev == HalfOpen && next == Closed:
		return log.InfoLevel
	default:
		return log.InfoLevel
	}
}

// installCircuitListeners wires OnOpen/OnClose/OnHalfOpen on the
// circuitbreaker builder so all automatic transitions emit (1) a log
// event and (2) a bus event when a publisher is configured. Called
// from buildCircuitBreaker.
//
// Emission order is log-first then bus: the existing log channel
// is the primary record; bus emission is opt-in and best-effort.
// "auto" trigger covers everything routed through the underlying
// circuit's state machine — including the listener fired by manual
// Trip → cb.Open() and Reset → cb.Close(); the dedicated logTrip
// path adds the manual-only TrippedPayload event for operator
// audit on top of those.
func installCircuitListeners(
	bldr circuitbreaker.Builder[any],
	b *breakerImpl,
) circuitbreaker.Builder[any] {
	emit := func(level log.Level, prev, next State) {
		b.logger().Log(level, "breaker state transition",
			"breaker.name", b.name,
			"breaker.state", next.String(),
			"breaker.prev_state", prev.String(),
			"breaker.policy", "circuit",
			"breaker.trips_total", b.tripsSnapshot(),
		)
		b.publishTransition(prev, next, "auto")
	}

	return bldr.
		OnOpen(func(e circuitbreaker.StateChangedEvent) {
			prev := mapState(e.OldState)
			emit(transitionLevel(prev, Open), prev, Open)
		}).
		OnClose(func(e circuitbreaker.StateChangedEvent) {
			prev := mapState(e.OldState)
			emit(transitionLevel(prev, Closed), prev, Closed)
		}).
		OnHalfOpen(func(e circuitbreaker.StateChangedEvent) {
			prev := mapState(e.OldState)
			emit(transitionLevel(prev, HalfOpen), prev, HalfOpen)
		})
}
