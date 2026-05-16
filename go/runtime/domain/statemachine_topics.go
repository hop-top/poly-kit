package domain

import (
	"fmt"

	"hop.top/kit/go/runtime/bus"
)

// StateMachineTopics holds the per-action topic strings emitted by
// StateMachine.Transition.
//
// StateMachine publishes one event per phase: PreTransitioned (sync,
// veto-able) before the transition and PostTransitioned (fire-and-
// forget) after success. Adopters override individual phases with
// WithSMTopics or replace both with WithSMTopicPrefix.
type StateMachineTopics struct {
	PreTransitioned  bus.Topic
	PostTransitioned bus.Topic
}

// DefaultStateMachineTopics is the kit baseline used when no override
// is supplied. Each topic conforms to the kit 4-segment past-tense
// convention and would pass bus.ValidateTopic.
var DefaultStateMachineTopics = StateMachineTopics{
	PreTransitioned:  "kit.runtime.state.pre_transitioned",
	PostTransitioned: "kit.runtime.state.post_transitioned",
}

// stateMachineActions is the canonical action list passed to
// bus.PrefixTopics when expanding a 3-segment prefix. Order is fixed
// so error messages from PrefixTopics report a predictable
// first-failing action.
var stateMachineActions = []string{"pre_transitioned", "post_transitioned"}

// SMOption configures a StateMachine via functional options. Named
// SMOption (not Option) to avoid collision with Service[T]'s generic
// Option in the same package.
type SMOption func(*StateMachine)

// WithSMTopicPrefix sets both StateMachine topics from a 3-segment
// prefix of the form "source.category.object". The composed topics
// are "<prefix>.pre_transitioned" and "<prefix>.post_transitioned".
//
// Example:
//
//	domain.NewStateMachine(rules, pub,
//	    domain.WithSMTopicPrefix("myapp.task.state"))
//
// Panics if prefix fails bus.PrefixTopics validation. Constructors
// are wired at boot, so a misconfigured prefix is a programmer error
// — fail-loud is preferred over silent default fallback that would
// hide subscribers missing events at runtime.
func WithSMTopicPrefix(prefix string) SMOption {
	tm, err := bus.PrefixTopics(prefix, stateMachineActions)
	if err != nil {
		panic(fmt.Sprintf("domain.WithSMTopicPrefix(%q): %v", prefix, err))
	}
	t := StateMachineTopics{
		PreTransitioned:  tm["pre_transitioned"],
		PostTransitioned: tm["post_transitioned"],
	}
	return func(sm *StateMachine) { sm.topics = t }
}

// WithSMTopics replaces individual phase topics. Empty bus.Topic
// fields keep the corresponding DefaultStateMachineTopics value, so
// callers can override a single phase without restating the other.
//
// Example:
//
//	domain.NewStateMachine(rules, pub,
//	    domain.WithSMTopics(domain.StateMachineTopics{
//	        PreTransitioned: "myapp.task.state.pre_transitioned",
//	    }))
func WithSMTopics(t StateMachineTopics) SMOption {
	if t.PreTransitioned == "" {
		t.PreTransitioned = DefaultStateMachineTopics.PreTransitioned
	}
	if t.PostTransitioned == "" {
		t.PostTransitioned = DefaultStateMachineTopics.PostTransitioned
	}
	return func(sm *StateMachine) { sm.topics = t }
}
