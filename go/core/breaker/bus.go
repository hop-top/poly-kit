package breaker

import (
	"fmt"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
)

// Topics holds the per-action topic strings emitted by the Breaker
// lifecycle.
//
// Breaker publishes one event per lifecycle moment: Tripped on manual
// Trip(reason); Opened, Closed, HalfOpened on the corresponding
// automatic state transitions surfaced by the underlying circuit
// breaker. Adopters override individual actions with WithTopics or
// replace all four with WithTopicPrefix.
type Topics struct {
	Tripped    bus.Topic
	Opened     bus.Topic
	Closed     bus.Topic
	HalfOpened bus.Topic
}

// DefaultTopics is the kit baseline used when no override is supplied.
// Each topic conforms to the kit 4-segment past-tense convention and
// would pass bus.ValidateTopic. "tripped", "opened", "closed", and
// "half_opened" are all in bus.pastTenseWhitelist.
var DefaultTopics = Topics{
	Tripped:    "kit.core.breaker.tripped",
	Opened:     "kit.core.breaker.opened",
	Closed:     "kit.core.breaker.closed",
	HalfOpened: "kit.core.breaker.half_opened",
}

// breakerActions is the canonical action list passed to bus.PrefixTopics
// when expanding a 3-segment prefix. Order is fixed so error messages
// from PrefixTopics report a predictable first-failing action.
var breakerActions = []string{"tripped", "opened", "closed", "half_opened"}

// TrippedPayload is the event payload for manual Trip(reason) calls.
// Trigger is implicitly "manual" — it's the only path that produces
// this payload — so a separate field would be redundant.
type TrippedPayload struct {
	Reason string
	From   State
	To     State
}

// TransitionPayload is the event payload for automatic state
// transitions (Opened/Closed/HalfOpened).
//
// Trigger describes what produced the transition:
//   - "auto"   — the circuit's own state machine transitioned (e.g.
//     failure threshold reached, delay elapsed, half-open probe)
//   - "manual" — direct caller action (currently unused; reserved so
//     future operator paths like ForceOpen can reuse this payload
//     shape rather than introducing a third struct)
type TransitionPayload struct {
	From    State
	To      State
	Trigger string
}

// WithPublisher supplies the EventPublisher used to emit lifecycle
// events. When unset (nil), no bus events are emitted — the kit/log
// channel still fires. Both channels operate independently;
// publisher errors do not block the breaker.
func WithPublisher(p domain.EventPublisher) Option {
	return func(c *config) { c.publisher = p }
}

// WithTopicPrefix sets all four breaker topics from a 3-segment
// prefix of the form "source.category.object". The composed topics
// are "<prefix>.tripped", "<prefix>.opened", "<prefix>.closed", and
// "<prefix>.half_opened".
//
// Example:
//
//	breaker.New("api",
//	    breaker.WithPublisher(pub),
//	    breaker.WithTopicPrefix("myapp.core.breaker"))
//
// Panics if prefix fails bus.PrefixTopics validation. Constructors
// are wired at boot, so a misconfigured prefix is a programmer error
// — fail-loud is preferred over silent default fallback that would
// hide subscribers missing events at runtime.
func WithTopicPrefix(prefix string) Option {
	tm, err := bus.PrefixTopics(prefix, breakerActions)
	if err != nil {
		panic(fmt.Sprintf("breaker.WithTopicPrefix(%q): %v", prefix, err))
	}
	t := Topics{
		Tripped:    tm["tripped"],
		Opened:     tm["opened"],
		Closed:     tm["closed"],
		HalfOpened: tm["half_opened"],
	}
	return func(c *config) { c.topics = t }
}

// WithTopics replaces individual action topics. Empty bus.Topic
// fields keep the corresponding DefaultTopics value, so callers can
// override a single action without restating the others.
//
// Example:
//
//	breaker.New("api",
//	    breaker.WithPublisher(pub),
//	    breaker.WithTopics(breaker.Topics{
//	        Tripped: "myapp.core.breaker.tripped",
//	    }))
func WithTopics(t Topics) Option {
	if t.Tripped == "" {
		t.Tripped = DefaultTopics.Tripped
	}
	if t.Opened == "" {
		t.Opened = DefaultTopics.Opened
	}
	if t.Closed == "" {
		t.Closed = DefaultTopics.Closed
	}
	if t.HalfOpened == "" {
		t.HalfOpened = DefaultTopics.HalfOpened
	}
	return func(c *config) { c.topics = t }
}

// topicForState returns the Topic to publish for an automatic
// transition into next. Closed-mapped fall-throughs (e.g. an unknown
// State enum) get the Closed topic — matching mapState's default.
func (t Topics) topicForState(next State) bus.Topic {
	switch next {
	case Open:
		return t.Opened
	case HalfOpen:
		return t.HalfOpened
	default:
		return t.Closed
	}
}
