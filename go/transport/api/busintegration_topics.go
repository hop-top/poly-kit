package api

import (
	"fmt"

	"hop.top/kit/go/runtime/bus"
)

// Topics names the bus topics emitted by the bus-integration middleware.
// All topics conform to the kit 4-segment past-tense convention
// (source.category.object.action) — see bus.ValidateTopic.
type Topics struct {
	RequestStart bus.Topic // semantic: request initiated  (default kit.api.request.started)
	RequestEnd   bus.Topic // semantic: request completed (default kit.api.request.ended)
}

// DefaultTopics is the conformant default topic set.
//
// Note: prior to T-0122 the middleware emitted "api.request.start" and
// "api.request.end" — both non-conformant (3 segments, present-tense).
// Those topics have been removed with no back-compat alias.
var DefaultTopics = Topics{
	RequestStart: "kit.api.request.started",
	RequestEnd:   "kit.api.request.ended",
}

// Option configures the bus-integration middleware.
type Option func(*config)

// config holds the resolved middleware configuration.
type config struct {
	topics Topics
}

// newConfig returns a config seeded with DefaultTopics, then applies opts.
func newConfig(opts ...Option) *config {
	c := &config{topics: DefaultTopics}
	for _, o := range opts {
		o(c)
	}
	return c
}

// WithTopicPrefix replaces the 3-segment topic prefix on all default
// topics. The given prefix MUST be 3 lowercase segments (e.g.
// "myapp.api.request"); the 4th action segment ("started"/"ended") is
// appended automatically.
//
// Per-field overrides via WithTopics take precedence over the prefix.
//
// Example:
//
//	WithTopicPrefix("myapp.api.request")
//	  → RequestStart: "myapp.api.request.started"
//	  → RequestEnd:   "myapp.api.request.ended"
//
// An invalid prefix is captured at construction time and surfaces as
// a panic when the middleware is wired onto a Router; this matches the
// strict-construction stance of bus.ValidateTopic so misconfiguration
// fails loudly rather than silently emitting bad topics.
func WithTopicPrefix(prefix string) Option {
	return func(c *config) {
		tm, err := bus.PrefixTopics(prefix, []string{"started", "ended"})
		if err != nil {
			panic(fmt.Errorf("api.WithTopicPrefix(%q): %w", prefix, err))
		}
		c.topics.RequestStart = tm["started"]
		c.topics.RequestEnd = tm["ended"]
	}
}

// WithTopics overrides individual topics. Empty fields in t leave the
// corresponding default (or previously-set) topic untouched, so callers
// can override just one of RequestStart/RequestEnd. Non-empty topics are
// validated; invalid topics panic at wire time (see WithTopicPrefix).
func WithTopics(t Topics) Option {
	return func(c *config) {
		if t.RequestStart != "" {
			if err := bus.ValidateTopic(t.RequestStart); err != nil {
				panic(fmt.Errorf("api.WithTopics RequestStart: %w", err))
			}
			c.topics.RequestStart = t.RequestStart
		}
		if t.RequestEnd != "" {
			if err := bus.ValidateTopic(t.RequestEnd); err != nil {
				panic(fmt.Errorf("api.WithTopics RequestEnd: %w", err))
			}
			c.topics.RequestEnd = t.RequestEnd
		}
	}
}
