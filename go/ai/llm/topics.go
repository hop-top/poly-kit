package llm

import (
	"fmt"
	"strings"

	"hop.top/kit/go/runtime/bus"
)

// Topics groups every bus topic the [Client] publishes. Adopters
// override individual entries via [WithTopics] or rebrand the whole
// set with [WithTopicPrefix].
//
// The action segments are NON-uniform on purpose: each event names the
// real verb that occurred (response.received vs request.started vs
// eva.evaluated). [WithTopicPrefix] preserves the trailing
// object.action pair when re-prefixing.
type Topics struct {
	RequestStart bus.Topic
	RequestEnd   bus.Topic
	RequestError bus.Topic
	Fallback     bus.Topic
	Route        bus.Topic
	EvaResult    bus.Topic
}

// DefaultTopics holds the canonical kit.ai.* topic strings. Package-
// level constants TopicRequestStart, TopicRequestEnd, etc. read their
// values from this struct so there is exactly one source of truth.
var DefaultTopics = Topics{
	RequestStart: "kit.ai.request.started",
	RequestEnd:   "kit.ai.response.received",
	RequestError: "kit.ai.request.errored",
	Fallback:     "kit.ai.fallback.applied",
	Route:        "kit.ai.route.selected",
	EvaResult:    "kit.ai.eva.evaluated",
}

// suffixOf returns the trailing "object.action" of a 4-segment topic.
// Panics if t does not have at least 2 segments — defaults are checked
// at package init via [DefaultTopics] so callers passing through
// [WithTopicPrefix] always have valid input.
func suffixOf(t bus.Topic) string {
	s := string(t)
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		panic(fmt.Sprintf("llm: topic %q has fewer than 2 segments", s))
	}
	return parts[len(parts)-2] + "." + parts[len(parts)-1]
}

// composeTopics builds a Topics struct by joining prefix with the
// object.action suffix of each [DefaultTopics] field. Each composed
// value is validated via [bus.ValidateTopic]; the first error short-
// circuits.
func composeTopics(prefix string) (Topics, error) {
	if prefix == "" {
		return Topics{}, fmt.Errorf("llm: topic prefix is empty")
	}
	if strings.HasSuffix(prefix, ".") {
		return Topics{}, fmt.Errorf("llm: topic prefix %q must not end with '.'", prefix)
	}
	parts := strings.Split(prefix, ".")
	if len(parts) != 2 {
		return Topics{}, fmt.Errorf(
			"llm: topic prefix %q has %d segments; expected 2 (source.category)",
			prefix, len(parts),
		)
	}
	out := Topics{
		RequestStart: bus.Topic(prefix + "." + suffixOf(DefaultTopics.RequestStart)),
		RequestEnd:   bus.Topic(prefix + "." + suffixOf(DefaultTopics.RequestEnd)),
		RequestError: bus.Topic(prefix + "." + suffixOf(DefaultTopics.RequestError)),
		Fallback:     bus.Topic(prefix + "." + suffixOf(DefaultTopics.Fallback)),
		Route:        bus.Topic(prefix + "." + suffixOf(DefaultTopics.Route)),
		EvaResult:    bus.Topic(prefix + "." + suffixOf(DefaultTopics.EvaResult)),
	}
	for _, t := range []bus.Topic{
		out.RequestStart, out.RequestEnd, out.RequestError,
		out.Fallback, out.Route, out.EvaResult,
	} {
		if err := bus.ValidateTopic(t); err != nil {
			return Topics{}, fmt.Errorf("llm: WithTopicPrefix(%q): %w", prefix, err)
		}
	}
	return out, nil
}

// WithTopicPrefix replaces the source.category segments of every
// published topic with prefix while preserving each event's
// object.action suffix. prefix MUST be exactly 2 dot-separated
// segments (e.g. "myapp.ai"); panics on invalid input so adopter
// wiring fails loudly at construction.
//
// Example:
//
//	llm.WithTopicPrefix("myapp.ai")
//	  → RequestStart = "myapp.ai.request.started"
//	  → RequestEnd   = "myapp.ai.response.received"
//	  → EvaResult    = "myapp.ai.eva.evaluated"
func WithTopicPrefix(prefix string) Option {
	t, err := composeTopics(prefix)
	if err != nil {
		panic(err)
	}
	return func(c *clientConfig) {
		c.topics = t
	}
}

// WithTopics overrides individual Topics entries on the [Client].
// Empty fields fall back to [DefaultTopics]; non-empty fields are
// validated via [bus.ValidateTopic] and panic on invalid input.
func WithTopics(t Topics) Option {
	merged := DefaultTopics
	if t.RequestStart != "" {
		if err := bus.ValidateTopic(t.RequestStart); err != nil {
			panic(fmt.Errorf("llm: WithTopics RequestStart: %w", err))
		}
		merged.RequestStart = t.RequestStart
	}
	if t.RequestEnd != "" {
		if err := bus.ValidateTopic(t.RequestEnd); err != nil {
			panic(fmt.Errorf("llm: WithTopics RequestEnd: %w", err))
		}
		merged.RequestEnd = t.RequestEnd
	}
	if t.RequestError != "" {
		if err := bus.ValidateTopic(t.RequestError); err != nil {
			panic(fmt.Errorf("llm: WithTopics RequestError: %w", err))
		}
		merged.RequestError = t.RequestError
	}
	if t.Fallback != "" {
		if err := bus.ValidateTopic(t.Fallback); err != nil {
			panic(fmt.Errorf("llm: WithTopics Fallback: %w", err))
		}
		merged.Fallback = t.Fallback
	}
	if t.Route != "" {
		if err := bus.ValidateTopic(t.Route); err != nil {
			panic(fmt.Errorf("llm: WithTopics Route: %w", err))
		}
		merged.Route = t.Route
	}
	if t.EvaResult != "" {
		if err := bus.ValidateTopic(t.EvaResult); err != nil {
			panic(fmt.Errorf("llm: WithTopics EvaResult: %w", err))
		}
		merged.EvaResult = t.EvaResult
	}
	return func(c *clientConfig) {
		c.topics = merged
	}
}
