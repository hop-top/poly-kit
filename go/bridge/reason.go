package bridge

// NoMatchReason names the rule that left a payload without a
// handler. The set is closed and stable: consumers switch on these
// values to decide how to report a failed dispatch, and a renamed
// value is a breaking change to every such consumer.
//
// Exactly one reason is recorded per unmatched payload. When several
// rules would fire across the installed manifests, the one earliest
// in [noMatchPrecedence] wins, so the answer to "why did nothing
// handle this?" does not depend on the order manifests happen to sit
// on disk.
//
// This mirrors [hop.top/kit/go/ai/cmdreflect.NonInvocableReason]: a
// closed string type with a precedence order, an IsValid guard and a
// one-line Explain, rather than a bare nil or a formatted string a
// caller would have to parse.
type NoMatchReason string

const (
	// ReasonMatched is the zero value, carried by every Match
	// result that found a handler. It is not a reason; it is the
	// absence of one.
	ReasonMatched NoMatchReason = ""

	// ReasonNoManifests marks a dispatch attempted with an empty
	// manifest set. Nothing is installed, so no rule could have
	// fired. A fresh install with no consumer CLIs hits this, and
	// a shell should report it as "install a CLI" rather than
	// "this payload is unsupported".
	ReasonNoManifests NoMatchReason = "no-manifests"

	// ReasonInvalidPayload marks a payload that sets zero or more
	// than one content kind. [Payload.UnmarshalJSON] rejects such
	// messages off the wire, so a payload reaching Match in this
	// state was built in Go, and the defect is in the caller, not
	// in the installed manifests.
	ReasonInvalidPayload NoMatchReason = "invalid-payload"

	// ReasonNoKindRule marks a payload whose oneof variant no
	// installed rule declares at all: a blob arrives and every
	// manifest accepts only urls and text. Installing a CLI that
	// accepts the kind is the fix.
	ReasonNoKindRule NoMatchReason = "no-kind-rule"

	// ReasonSchemeMismatch marks a url payload whose scheme no
	// candidate rule lists. Every rule of the right kind was
	// considered and each declared a different scheme set, so the
	// payload is well-formed and simply out of scope for what is
	// installed.
	ReasonSchemeMismatch NoMatchReason = "scheme-mismatch"

	// ReasonMIMEMismatch marks a text, file or blob payload whose
	// MIME type no candidate rule's patterns cover. A payload with
	// no MIME hint at all also lands here: a rule declaring
	// patterns cannot confirm an unknown type matches them.
	ReasonMIMEMismatch NoMatchReason = "mime-mismatch"

	// ReasonTooLarge marks a payload rejected on size by every
	// candidate that otherwise matched. It is the one reason a
	// shell can act on directly — the payload is exactly what the
	// handler wants, only bigger than it accepts — so it must not
	// be collapsed into a generic "no handler".
	ReasonTooLarge NoMatchReason = "too-large"
)

// noMatchPrecedence orders the reasons from most to least specific.
// A dispatch where one candidate rejected on scheme and another on
// size reports too-large, because the size rejection is the more
// actionable answer: it names a handler that wanted this payload.
//
// Structural facts come first: no manifests installed and a
// malformed payload are not judgements about the payload's content,
// and reporting either as a content mismatch would misdirect the
// operator. ReasonNoKindRule follows — nothing of the right kind
// exists, so no filter ever ran. The three filter reasons come last,
// ordered by how actionable they are: size first (shrink it), then
// MIME, then scheme.
var noMatchPrecedence = []NoMatchReason{
	ReasonNoManifests,
	ReasonInvalidPayload,
	ReasonNoKindRule,
	ReasonTooLarge,
	ReasonMIMEMismatch,
	ReasonSchemeMismatch,
}

// AllNoMatchReasons returns every defined reason in precedence
// order, excluding ReasonMatched. Callers rendering a legend or
// asserting exhaustive handling iterate this rather than hard-coding
// a list, so a reason added here is picked up automatically.
func AllNoMatchReasons() []NoMatchReason {
	return append([]NoMatchReason(nil), noMatchPrecedence...)
}

// String returns the reason as written on the wire.
func (r NoMatchReason) String() string { return string(r) }

// IsValid reports whether r is ReasonMatched or one of the defined
// reasons.
func (r NoMatchReason) IsValid() bool {
	if r == ReasonMatched {
		return true
	}
	for _, x := range noMatchPrecedence {
		if x == r {
			return true
		}
	}
	return false
}

// Explain returns a one-line human-readable rationale for r,
// suitable for an error message or a log line. Returns the empty
// string for ReasonMatched.
func (r NoMatchReason) Explain() string {
	switch r {
	case ReasonNoManifests:
		return "no manifests installed: nothing could handle any payload"
	case ReasonInvalidPayload:
		return "payload sets zero or multiple content kinds"
	case ReasonNoKindRule:
		return "no installed rule accepts this payload's kind"
	case ReasonSchemeMismatch:
		return "no rule accepting this kind lists the payload's URL scheme"
	case ReasonMIMEMismatch:
		return "no rule accepting this kind matches the payload's MIME type"
	case ReasonTooLarge:
		return "payload exceeds every matching rule's max_size"
	}
	return ""
}

// pickNoMatchReason returns the highest-precedence reason among the
// candidates, or ReasonMatched when none fired.
func pickNoMatchReason(fired map[NoMatchReason]bool) NoMatchReason {
	for _, r := range noMatchPrecedence {
		if fired[r] {
			return r
		}
	}
	return ReasonMatched
}
