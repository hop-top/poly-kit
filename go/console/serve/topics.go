package serve

import "hop.top/kit/go/runtime/bus"

// DefaultTopicPrefix is the 2-segment source.category prefix serve
// events publish under. Rebrandable by adopters the same way every
// other kit emitter's prefix is (see docs/contracts/event-topics.md).
const DefaultTopicPrefix = "kit.serve"

// Action segments for serve lifecycle events (contract
// §"Surfaced events"). Each satisfies bus.ValidateTopic without
// extending the past-tense whitelist: started/failed/stopped are
// listed there, and ready_reported is a snake_case multi-word action
// that satisfies the "ed" heuristic on the whole segment — the same
// shape as pre_transitioned.
//
// A bare "ready" does not validate. Do not introduce one.
const (
	ActionStarted       = "started"
	ActionReadyReported = "ready_reported"
	ActionFailed        = "failed"
	ActionStopped       = "stopped"
)

// Object segments. The service identifier travels in the event
// payload, not in the topic, so subscribers are not forced to re-bind
// when a tool gains a service.
const (
	ObjectService    = "service"
	ObjectSupervisor = "supervisor"
)

// DefaultTopics returns the conformant topic set for prefix, which is
// a 2-segment source.category string such as DefaultTopicPrefix.
// Keys are "<object>.<action>".
//
// Every returned topic has already passed bus.ValidateTopic by
// construction; a prefix that would produce a non-conformant topic
// makes bus.TopicOf panic at wiring time rather than silently
// dropping events at runtime.
func DefaultTopics(prefix string) bus.TopicMap {
	if prefix == "" {
		prefix = DefaultTopicPrefix
	}
	source, category := splitPrefix(prefix)
	out := bus.TopicMap{}
	for _, spec := range []struct {
		object string
		action string
	}{
		{ObjectService, ActionStarted},
		{ObjectService, ActionReadyReported},
		{ObjectService, ActionFailed},
		{ObjectService, ActionStopped},
		{ObjectSupervisor, ActionReadyReported},
		{ObjectSupervisor, ActionStopped},
	} {
		out[spec.object+"."+spec.action] = bus.TopicOf(source, category, spec.object).Action(spec.action)
	}
	return out
}

// splitPrefix splits a "source.category" prefix. A prefix without a
// dot is taken as the source with the default category, so a
// mis-typed prefix still produces a validatable topic rather than a
// panic deep inside the builder.
func splitPrefix(prefix string) (source, category string) {
	for i := 0; i < len(prefix); i++ {
		if prefix[i] == '.' {
			return prefix[:i], prefix[i+1:]
		}
	}
	return prefix, "serve"
}
