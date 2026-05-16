package bus

import (
	"errors"
	"fmt"
	"strings"
)

// Mode controls how the bus enforces topic naming on Publish.
//
// The naming contract is the 4-segment form
// "[Source].[Category].[Object].[Action]" — see [Validate] for the
// exact rules.
type Mode int

const (
	// ModeOff disables topic validation. Publish never rejects on
	// naming and never emits warnings.
	ModeOff Mode = iota
	// ModeWarn validates topics and reports violations via the
	// configured reporter, but still delivers the event.
	ModeWarn
	// ModeStrict validates topics and rejects publishes that
	// violate the naming contract; the event is not delivered.
	ModeStrict
)

// String returns a stable lowercase name for the mode.
func (m Mode) String() string {
	switch m {
	case ModeOff:
		return "off"
	case ModeWarn:
		return "warn"
	case ModeStrict:
		return "strict"
	default:
		return fmt.Sprintf("mode(%d)", int(m))
	}
}

// ErrInvalidTopic is the sentinel for topic-naming violations. Use
// errors.Is to test against it. Validate wraps it with the offending
// topic and a human-readable reason.
var ErrInvalidTopic = errors.New("bus: invalid topic")

// InvalidTopicError is the rich error returned by Validate. It wraps
// [ErrInvalidTopic] and carries the offending topic plus the reason
// the topic failed validation.
type InvalidTopicError struct {
	Topic  Topic
	Reason string
}

func (e *InvalidTopicError) Error() string {
	return fmt.Sprintf("%s: %q (%s)", ErrInvalidTopic.Error(), string(e.Topic), e.Reason)
}

// Unwrap returns ErrInvalidTopic so errors.Is(err, ErrInvalidTopic) works.
func (e *InvalidTopicError) Unwrap() error { return ErrInvalidTopic }

// maxTopicLen caps the total length of a published topic.
const maxTopicLen = 128

// Validate checks that t conforms to the published-topic naming
// contract:
//
//   - exactly 4 segments separated by '.'
//   - each segment matches ^[a-z][a-z0-9_]*$
//   - total length <= 128 characters
//   - wildcards ('*', '#') are NEVER permitted in published topics
//
// Validate is for published topics only. It does not affect
// [Topic.Match] semantics; subscribe patterns retain wildcard support.
//
// Returns nil for valid topics, or an *[InvalidTopicError] (which
// wraps [ErrInvalidTopic]) describing the first violation found.
func Validate(t Topic) error {
	s := string(t)
	if s == "" {
		return &InvalidTopicError{Topic: t, Reason: "empty topic"}
	}
	if len(s) > maxTopicLen {
		return &InvalidTopicError{Topic: t, Reason: fmt.Sprintf("length %d exceeds max %d", len(s), maxTopicLen)}
	}
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return &InvalidTopicError{
			Topic:  t,
			Reason: fmt.Sprintf("expected 4 segments [Source].[Category].[Object].[Action], got %d", len(parts)),
		}
	}
	for i, p := range parts {
		if err := validateSegment(p); err != nil {
			return &InvalidTopicError{
				Topic:  t,
				Reason: fmt.Sprintf("segment %d (%q): %s", i+1, p, err),
			}
		}
	}
	return nil
}

// checkTopic applies the configured enforcement mode to t. It is the
// single point where Publish enforces topic naming.
//
// Behavior:
//   - ModeOff: returns nil; reporter is not invoked.
//   - ModeWarn: invokes reporter on validation failure and returns nil
//     so the publish proceeds.
//   - ModeStrict: invokes reporter on validation failure and returns
//     the error so the publish is aborted.
//
// reporter MUST be non-nil; New installs a no-op default.
func checkTopic(mode Mode, reporter ErrFunc, t Topic) error {
	if mode == ModeOff {
		return nil
	}
	err := Validate(t)
	if err == nil {
		return nil
	}
	if reporter != nil {
		reporter(err)
	}
	if mode == ModeStrict {
		return err
	}
	return nil
}

// validateSegment enforces ^[a-z][a-z0-9_]*$ on a single segment and
// rejects wildcards explicitly so the error message is friendly.
func validateSegment(seg string) error {
	if seg == "" {
		return errors.New("empty segment")
	}
	if seg == "*" || seg == "#" {
		return errors.New("wildcards are not allowed in published topics")
	}
	for i, r := range seg {
		switch {
		case r >= 'a' && r <= 'z':
			// always allowed
		case r >= '0' && r <= '9':
			if i == 0 {
				return errors.New("must start with a lowercase letter")
			}
		case r == '_':
			if i == 0 {
				return errors.New("must start with a lowercase letter")
			}
		default:
			return fmt.Errorf("invalid character %q (allowed: a-z, 0-9, _)", r)
		}
	}
	return nil
}
