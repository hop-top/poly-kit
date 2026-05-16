package bus

import (
	"fmt"
	"strings"
)

// TopicMap maps an action key (e.g. "created") to a fully-qualified Topic
// (e.g. "kit.runtime.entity.created"). Modules that publish multiple
// related events expose a TopicMap-shaped struct to let adopters override
// individual entries.
type TopicMap map[string]Topic

// pastTenseWhitelist holds action segments that are valid past tense but
// don't match the simple "ends in 'ed'" heuristic, plus a few present-
// participle forms ("ing") used for in-flight signals.
//
// Adding to this list is the documented way to extend ValidateTopic when
// a new action verb doesn't fit the heuristic.
var pastTenseWhitelist = map[string]struct{}{
	"started":     {},
	"ended":       {},
	"succeeded":   {},
	"failed":      {},
	"canceled":    {},
	"snoozed":     {},
	"received":    {},
	"sent":        {},
	"applied":     {},
	"selected":    {},
	"evaluated":   {},
	"installed":   {},
	"downloaded":  {},
	"released":    {},
	"tripped":     {},
	"opened":      {},
	"closed":      {},
	"half_opened": {},
	"paid":        {},
	"made":        {},
	"built":       {},
	"read":        {},
	"set":         {},
	"put":         {},
	"hit":         {},
	"lost":        {},
	"found":       {},
	"won":         {},
}

// ValidateTopic returns nil when t conforms to the kit 4-segment
// past-tense convention:
//
//   - exactly 4 dot-separated segments: source.category.object.action
//   - all segments lowercase ASCII letters, digits, or underscores
//   - no leading/trailing dots
//   - no empty segments
//   - action segment is past-tense: ends in "ed", or in
//     pastTenseWhitelist (covers irregular forms + a few participles)
//
// Multi-word actions use snake_case (e.g. "pre_transitioned",
// "post_transitioned", "half_opened"). The "ed"-ending check uses the
// whole final segment, so "pre_transitioned" passes naturally.
//
// ValidateTopic is intentionally strict at construction time so that
// misconfigured WithTopicPrefix calls fail loudly during adopter wiring,
// not at runtime when subscribers fail to receive expected events.
func ValidateTopic(t Topic) error {
	s := string(t)
	if s == "" {
		return fmt.Errorf("topic is empty (expected source.category.object.action)")
	}
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return fmt.Errorf(
			"topic %q has %d segments; expected 4 (source.category.object.action)",
			s, len(parts),
		)
	}
	for i, seg := range parts {
		if seg == "" {
			return fmt.Errorf("topic %q has empty segment at position %d", s, i)
		}
		if !validSegment(seg) {
			return fmt.Errorf(
				"topic %q segment %q must be lowercase letters, digits, or underscores",
				s, seg,
			)
		}
	}
	action := parts[3]
	if !isPastTense(action) {
		return fmt.Errorf(
			"topic %q action segment %q is not past-tense (e.g. \"started\", \"created\"); see bus.PastTenseWhitelist",
			s, action,
		)
	}
	return nil
}

// PrefixTopics builds a TopicMap from a 3-segment prefix and a slice of
// past-tense action segments. The composed topic for each action is
// "<prefix>.<action>"; each is validated via ValidateTopic.
//
// Example:
//
//	PrefixTopics("wsm.runtime.workspace", []string{"created", "updated"})
//	  → TopicMap{
//	      "created": "wsm.runtime.workspace.created",
//	      "updated": "wsm.runtime.workspace.updated",
//	    }
//
// Returns the partial map and the first validation error encountered.
// Callers that want strict-or-empty semantics should treat any non-nil
// error as fatal.
func PrefixTopics(prefix string, actions []string) (TopicMap, error) {
	if prefix == "" {
		return nil, fmt.Errorf("prefix is empty")
	}
	if strings.HasSuffix(prefix, ".") {
		return nil, fmt.Errorf("prefix %q must not end with '.'", prefix)
	}
	parts := strings.Split(prefix, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf(
			"prefix %q has %d segments; expected 3 (source.category.object)",
			prefix, len(parts),
		)
	}
	for i, seg := range parts {
		if seg == "" {
			return nil, fmt.Errorf("prefix %q has empty segment at position %d", prefix, i)
		}
		if !validSegment(seg) {
			return nil, fmt.Errorf(
				"prefix %q segment %q must be lowercase letters, digits, or underscores",
				prefix, seg,
			)
		}
	}
	out := make(TopicMap, len(actions))
	for _, a := range actions {
		topic := Topic(prefix + "." + a)
		if err := ValidateTopic(topic); err != nil {
			return out, fmt.Errorf("PrefixTopics(%q, %v): %w", prefix, actions, err)
		}
		out[a] = topic
	}
	return out, nil
}

// validSegment reports whether seg consists only of lowercase ASCII
// letters, digits, and underscores.
func validSegment(seg string) bool {
	for _, r := range seg {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return len(seg) > 0
}

// isPastTense reports whether action is a recognized past-tense verb
// per the heuristic + whitelist.
func isPastTense(action string) bool {
	if strings.HasSuffix(action, "ed") {
		return true
	}
	if _, ok := pastTenseWhitelist[action]; ok {
		return true
	}
	return false
}
