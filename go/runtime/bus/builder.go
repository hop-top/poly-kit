package bus

import (
	"fmt"
	"strings"
)

// TopicBuilder is a value-typed builder for [Topic] strings that
// follow the 4-segment grammar [Source].[Category].[Object].[Action].
//
// The builder carries an optional snake_case modifier that joins
// into the Object segment with an underscore on render. The wire
// form for TopicOf("kit","config","snapshot").Mod("reload").Action("failed")
// is "kit.config.snapshot_reload.failed" — the modifier is NOT a
// 5th dot segment; it stays inside the Object position to preserve
// the 4-segment grammar.
//
// TopicBuilder is a value type and methods return a new value.
// Builders are safe to share across goroutines and to mutate via
// chained calls without affecting the caller's instance.
//
// See ADR-0017 (docs/adr/0017-bus-topic-naming-and-qualifiers.md)
// for the rationale behind the embedded-modifier choice.
type TopicBuilder struct {
	source   string
	category string
	object   string
	modifier string
}

// TopicOf constructs a [TopicBuilder] for the given Source, Category,
// and Object segments. None of the inputs are validated until
// [TopicBuilder.Action] is called — Action is the terminal method
// that produces the final [Topic] string and runs [ValidateTopic] on
// the result.
//
// To assemble a topic with a snake_case modifier on the Object
// segment, chain [TopicBuilder.Mod] before [TopicBuilder.Action]:
//
//	t := bus.TopicOf("kit", "config", "snapshot").
//	    Mod("reload").
//	    Action("failed")
//	// t == "kit.config.snapshot_reload.failed"
//
// TopicOf coexists with [PrefixTopics]: PrefixTopics expands a
// 3-segment prefix into a TopicMap of past-tense actions (the
// existing path used under the hood by every WithTopicPrefix
// option), while TopicOf is the typed-construction path for callers
// that build a single topic at a time.
func TopicOf(source, category, object string) TopicBuilder {
	return TopicBuilder{source: source, category: category, object: object}
}

// PrefixedTopicOf is a convenience wrapper for the common case where
// the (source, category, object) prefix is fixed and the caller
// optionally supplies a modifier inline.
//
// At most one modifier is honored (last-wins); excess modifier
// arguments are ignored. To build a topic with no modifier, omit
// the variadic argument entirely.
//
//	bus.PrefixedTopicOf("kit", "config", "snapshot").Action("reloaded")
//	// "kit.config.snapshot.reloaded"
//
//	bus.PrefixedTopicOf("kit", "config", "snapshot", "reload").Action("failed")
//	// "kit.config.snapshot_reload.failed"
func PrefixedTopicOf(source, category, object string, modifier ...string) TopicBuilder {
	b := TopicOf(source, category, object)
	if len(modifier) > 0 {
		b.modifier = modifier[len(modifier)-1]
	}
	return b
}

// Mod returns a copy of b with the modifier set to m. Calling Mod
// twice is last-wins: the most recent value replaces any prior
// modifier on the chain. Pass an empty string to clear a previously
// set modifier.
//
// The modifier itself is validated as a snake_case segment by
// [TopicBuilder.Action] — values containing uppercase letters or
// punctuation cause Action to panic.
func (b TopicBuilder) Mod(m string) TopicBuilder {
	b.modifier = m
	return b
}

// Source returns a copy of b with the Source segment replaced.
// Useful for callers that received a TopicBuilder from [ParseTopic]
// and want to retarget it to a different emitter without rebuilding.
func (b TopicBuilder) Source(s string) TopicBuilder {
	b.source = s
	return b
}

// Category returns a copy of b with the Category segment replaced.
func (b TopicBuilder) Category(c string) TopicBuilder {
	b.category = c
	return b
}

// Object returns a copy of b with the Object segment replaced.
// The modifier (if any) is preserved; clear it with Mod("") if the
// new object should stand alone.
func (b TopicBuilder) Object(o string) TopicBuilder {
	b.object = o
	return b
}

// SourceSeg / CategorySeg / ObjectSeg / ModifierSeg expose the raw
// builder segments for inspection (e.g. round-trip equality after
// [ParseTopic]).
func (b TopicBuilder) SourceSeg() string   { return b.source }
func (b TopicBuilder) CategorySeg() string { return b.category }
func (b TopicBuilder) ObjectSeg() string   { return b.object }
func (b TopicBuilder) ModifierSeg() string { return b.modifier }

// ParseTopic is the inverse of [TopicBuilder.Action]. It validates s
// via [ValidateTopic] (which enforces the 4-segment + past-tense
// rules) and returns a [TopicBuilder] populated with the parsed
// segments plus the action that was rendered.
//
// Object/modifier split rule: ParseTopic splits the Object segment
// on the FIRST underscore to separate object from modifier. This
// matches the wire form produced by [TopicBuilder.Action]:
//
//   - "snapshot"          → object="snapshot",  modifier=""
//   - "snapshot_reload"   → object="snapshot",  modifier="reload"
//   - "snapshot_partial_reload"
//     → object="snapshot",  modifier="partial_reload"
//
// The returned action is also returned so callers can re-render
// without remembering it: builder, action, _ := ParseTopic(s);
// _ = builder.Mod("retry").Action(action).
//
// On invalid input, ParseTopic returns the zero TopicBuilder, an
// empty action, and a non-nil error. When the failure is a topic
// grammar violation the error wraps *[InvalidTopicError] so callers
// can use errors.As / errors.Is to discriminate.
func ParseTopic(s string) (TopicBuilder, string, error) {
	t := Topic(s)
	if err := ValidateTopic(t); err != nil {
		return TopicBuilder{}, "", &InvalidTopicError{
			Topic:  t,
			Reason: err.Error(),
		}
	}
	parts := strings.SplitN(s, ".", 4)
	// ValidateTopic already guaranteed len(parts) == 4 via Split,
	// but SplitN with n=4 is equivalent here and defensive.
	if len(parts) != 4 {
		return TopicBuilder{}, "", &InvalidTopicError{
			Topic:  t,
			Reason: fmt.Sprintf("expected 4 segments, got %d", len(parts)),
		}
	}
	source, category, objectSeg, action := parts[0], parts[1], parts[2], parts[3]

	object := objectSeg
	modifier := ""
	if i := strings.Index(objectSeg, "_"); i >= 0 {
		object = objectSeg[:i]
		modifier = objectSeg[i+1:]
	}

	return TopicBuilder{
		source:   source,
		category: category,
		object:   object,
		modifier: modifier,
	}, action, nil
}

// Action renders the builder into a validated [Topic] string and
// returns it. Action is the terminal method on the builder chain.
//
// The modifier (when set) joins into the Object segment with a
// single underscore: TopicOf("kit","config","snapshot").Mod("reload").
// Action("failed") returns "kit.config.snapshot_reload.failed".
//
// Action runs [ValidateTopic] on the rendered string and PANICS on
// any validation failure. A panic from Action is always a caller
// bug — uppercase letters, punctuation, missing segments, or a
// non-past-tense action all surface as panics with a wrapped
// *[InvalidTopicError] payload so test failures point at the
// offending construction site.
//
// Validate vs ValidateTopic note: Action calls ValidateTopic, which
// is stricter than Validate (it adds the past-tense check on the
// Action segment). The two functions overlap — see ADR-0017 for
// the duplication discussion and the planned consolidation.
func (b TopicBuilder) Action(action string) Topic {
	object := b.object
	if b.modifier != "" {
		object = b.object + "_" + b.modifier
	}
	parts := []string{b.source, b.category, object, action}
	t := Topic(strings.Join(parts, "."))
	if err := ValidateTopic(t); err != nil {
		panic(fmt.Errorf("bus.TopicOf: %w", &InvalidTopicError{
			Topic:  t,
			Reason: err.Error(),
		}))
	}
	return t
}
