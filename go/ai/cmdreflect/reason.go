package cmdreflect

// NonInvocableReason names the rule that made a command
// non-invocable. The set is closed and stable: consumers switch on
// these values to decide how to present an excluded command, and a
// renamed value is a breaking change to every such consumer.
//
// Exactly one reason is recorded per command. When several rules
// would fire, the one earliest in [reasonPrecedence] wins, so the
// answer to "why can't I call this?" does not depend on evaluation
// order inside the walker.
type NonInvocableReason string

const (
	// ReasonNone is the zero value, carried by every command whose
	// Invocable is true. It is not a reason; it is the absence of
	// one.
	ReasonNone NonInvocableReason = ""

	// ReasonNotRunnable marks a command with no Run or RunE. Cobra
	// groups exist to hold children and print help; invoking one
	// does no work. Intermediate nodes in a deep tree carry this.
	ReasonNotRunnable NonInvocableReason = "not-runnable"

	// ReasonBuiltin marks a cobra or fang built-in: help,
	// completion and its per-shell children, man, and the
	// __complete shell-integration hooks. These belong to the CLI
	// framework, not to the adopter's surface, and describing them
	// as callable tools misleads an agent.
	ReasonBuiltin NonInvocableReason = "builtin"

	// ReasonHiddenInternal marks a command with Hidden set. Hidden
	// is an adopter's statement that a command is not part of the
	// supported surface. It stays reflected so an operator can see
	// it exists, and stays non-invocable so a transport does not
	// publish it.
	ReasonHiddenInternal NonInvocableReason = "hidden-internal"

	// ReasonDeprecated marks a command carrying any deprecation
	// marker: cobra's Deprecated string, kit/deprecated-since, or
	// kit/removal-target. The command still works on the CLI; it is
	// withheld from projected surfaces so agents do not latch onto
	// a command scheduled for removal.
	ReasonDeprecated NonInvocableReason = "deprecated"

	// ReasonInteractive marks a command whose resolved side-effect
	// tier is interactive: a shell, a TUI, a supervisor that blocks
	// until signaled. It needs a terminal and a human, so a
	// request/reply transport cannot satisfy its contract.
	ReasonInteractive NonInvocableReason = "interactive"

	// ReasonUnauthorizedDestructive marks a destructive command
	// that the resolved authorization rules do not permit on the
	// surface being reflected for. The command is real and the
	// caller simply lacks standing, which is a different answer
	// from "no such command" and must read differently to an agent.
	ReasonUnauthorizedDestructive NonInvocableReason = "unauthorized-destructive"

	// ReasonManagementOnly marks a command reserved to kit's own
	// management surface — the spec subcommand and the other
	// reserved depth-1 verbs a tool inherits rather than declares.
	// Publishing kit's introspection plumbing as adopter tools
	// dilutes the tool list without adding capability.
	ReasonManagementOnly NonInvocableReason = "management-only"

	// ReasonMalformedSchema marks a command whose declared metadata
	// does not resolve: an unrecognized kit/side-effect value, or a
	// kit/output-schema that is not valid JSON. The command is
	// described so the defect is visible, and withheld because a
	// consumer cannot build a correct request for it.
	ReasonMalformedSchema NonInvocableReason = "malformed-schema"
)

// reasonPrecedence orders the reasons from most to least specific.
// A command that is both hidden and deprecated reports
// hidden-internal, because hiding is the stronger statement about
// whether the command is part of the surface at all.
//
// Structural facts come first: a non-runnable group or a framework
// built-in is not a policy judgement and should never be reported as
// one. Declaration defects come next, because a malformed schema
// makes every downstream judgement unreliable. Surface withdrawal
// (hidden, management-only, deprecated) follows, and behavioral
// exclusions (interactive, unauthorized) come last — those describe
// a command that IS on the surface and still cannot be called here.
var reasonPrecedence = []NonInvocableReason{
	ReasonNotRunnable,
	ReasonBuiltin,
	ReasonMalformedSchema,
	ReasonHiddenInternal,
	ReasonManagementOnly,
	ReasonDeprecated,
	ReasonInteractive,
	ReasonUnauthorizedDestructive,
}

// AllReasons returns every defined reason in precedence order,
// excluding ReasonNone. Callers rendering a legend or asserting
// exhaustive handling iterate this rather than hard-coding a list,
// so a reason added here is picked up automatically.
func AllReasons() []NonInvocableReason {
	return append([]NonInvocableReason(nil), reasonPrecedence...)
}

// String returns the reason as written on the wire.
func (r NonInvocableReason) String() string { return string(r) }

// IsValid reports whether r is ReasonNone or one of the defined
// reasons.
func (r NonInvocableReason) IsValid() bool {
	if r == ReasonNone {
		return true
	}
	for _, x := range reasonPrecedence {
		if x == r {
			return true
		}
	}
	return false
}

// Explain returns a one-line human-readable rationale for r,
// suitable for an error message or a --list column. Returns the
// empty string for ReasonNone.
func (r NonInvocableReason) Explain() string {
	switch r {
	case ReasonNotRunnable:
		return "command group: has subcommands but no action of its own"
	case ReasonBuiltin:
		return "CLI framework built-in, not part of the tool's surface"
	case ReasonHiddenInternal:
		return "marked hidden: not part of the supported surface"
	case ReasonDeprecated:
		return "deprecated: withheld from projected surfaces"
	case ReasonInteractive:
		return "interactive: requires a terminal and a human"
	case ReasonUnauthorizedDestructive:
		return "destructive and not authorized on this surface"
	case ReasonManagementOnly:
		return "reserved to the tool's own management surface"
	case ReasonMalformedSchema:
		return "declared metadata does not resolve"
	}
	return ""
}

// pickReason returns the highest-precedence reason among the
// candidates, or ReasonNone when none fired.
func pickReason(candidates map[NonInvocableReason]bool) NonInvocableReason {
	for _, r := range reasonPrecedence {
		if candidates[r] {
			return r
		}
	}
	return ReasonNone
}
