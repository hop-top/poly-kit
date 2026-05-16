package cli

import (
	"fmt"
	"strings"

	"hop.top/kit/go/console/output"
)

// ValidationError reports leaf commands that fail Root.Validate.
//
// The original four buckets (Missing, Invalid, MissingIdempotency,
// InvalidIdempotency) are preserved verbatim for the side-effect +
// idempotency arms. Layer-A enforcement adds further
// buckets covering Short/Long, the kit/output-schema annotation,
// the reserved status subcommand, and the configurable gates
// (dry-run rationale, destructive token, guidance) plus the
// noun-verb shape pass.
type ValidationError struct {
	// Missing is the list of command paths lacking kit/side-effect.
	Missing []string
	// Invalid is the list of command paths whose kit/side-effect
	// tag is not one of the recognized values, formatted as
	// "<path>=<value>".
	Invalid []string
	// MissingIdempotency is the list of command paths lacking
	// kit/idempotent (after auto-apply has run). When this slice is
	// non-empty the adopter declined the kit default and never
	// supplied an explicit tag — the validator refuses to run the
	// CLI in that state.
	MissingIdempotency []string
	// InvalidIdempotency is the list of command paths whose
	// kit/idempotent tag is not one of the recognized values
	// (yes|no|conditional), formatted as "<path>=<value>".
	InvalidIdempotency []string

	// Layer-A additions follow. All gated on
	// Config.EnforceValidate (hard tier) or the corresponding
	// Config.Enforce* flag (configurable tier).

	// MissingShort lists command paths whose cmd.Short is empty.
	// Both runnable leaves and group (intermediate) nodes are
	// checked.
	MissingShort []string
	// MissingLong lists runnable leaf paths whose cmd.Long is empty.
	MissingLong []string
	// InvalidOutputSchema lists "<path>: <reason>" entries for
	// leaves whose kit/output-schema annotation is present but does
	// not parse as JSON.
	InvalidOutputSchema []string
	// MissingStatusSubcommand carries the root path when the
	// reserved `status` subcommand is not registered. Single-element
	// slice in practice (one root per Validate call).
	MissingStatusSubcommand []string
	// MissingDryRunRationale lists write|destructive paths annotated
	// kit/dry-run=opted-out without a paired kit/dry-run-rationale.
	// Populated only when Config.EnforceDryRunRationale is true.
	MissingDryRunRationale []string
	// MissingDestructiveToken lists destructive leaf paths without
	// kit/destructive-token=required. Populated only when
	// Config.EnforceDestructiveToken is true.
	MissingDestructiveToken []string
	// MissingExamples lists runnable leaf paths without
	// kit/examples. Populated only when Config.EnforceGuidance is
	// true.
	MissingExamples []string
	// MissingNextSteps lists non-read leaf paths without
	// kit/next-steps. Populated only when Config.EnforceGuidance is
	// true.
	MissingNextSteps []string
	// UnannotatedTopLevelLeaf lists depth-1 runnable leaf paths
	// without kit/top-level-verb=true.
	UnannotatedTopLevelLeaf []string
	// TooManyTopLevelVerbs carries a single string describing the
	// count + cap violation when the validator counts more depth-1
	// runnable leaves than Config.MaxTopLevelVerbs.
	TooManyTopLevelVerbs []string
	// UnannotatedDepthExceedance lists paths at depth >= 3 whose
	// chain of intermediate nodes is missing kit/hierarchical and
	// whose top-level ancestor is not reserved.
	UnannotatedDepthExceedance []string
	// HierarchyDepthExceeded lists paths whose depth exceeds the
	// effective Config.MaxHierarchyDepth (hard-capped at 5).
	HierarchyDepthExceeded []string
	// PassthroughRejected lists paths annotated kit/passthrough
	// when Config.PassthroughStrictness is "reject".
	PassthroughRejected []string
}

// Error returns a multi-line message that lists side-effect issues
// first, then idempotency issues, then the additional Layer-A
// buckets. Format is stable for adopter test assertions; new buckets
// append at the end in the order they were added.
func (e *ValidationError) Error() string {
	var b strings.Builder
	b.WriteString("cli validation failed")
	hasPrior := false
	appendBucket := func(label string, items []string) {
		if len(items) == 0 {
			return
		}
		if hasPrior {
			b.WriteString(";")
		} else {
			b.WriteString(":")
		}
		fmt.Fprintf(&b, " %d leaf command(s) %s: %s",
			len(items), label, strings.Join(items, ", "))
		hasPrior = true
	}
	appendBucket("missing kit/side-effect annotation", e.Missing)
	appendBucket("with invalid kit/side-effect", e.Invalid)
	appendBucket("missing kit/idempotent annotation", e.MissingIdempotency)
	appendBucket("with invalid kit/idempotent", e.InvalidIdempotency)
	appendBucket("missing Short", e.MissingShort)
	appendBucket("missing Long", e.MissingLong)
	appendBucket("with invalid kit/output-schema", e.InvalidOutputSchema)
	appendBucket("missing reserved 'status' subcommand", e.MissingStatusSubcommand)
	appendBucket("missing kit/dry-run-rationale", e.MissingDryRunRationale)
	appendBucket("missing kit/destructive-token", e.MissingDestructiveToken)
	appendBucket("missing kit/examples", e.MissingExamples)
	appendBucket("missing kit/next-steps", e.MissingNextSteps)
	appendBucket("depth-1 leaf missing kit/top-level-verb", e.UnannotatedTopLevelLeaf)
	appendBucket("exceeding MaxTopLevelVerbs", e.TooManyTopLevelVerbs)
	appendBucket("at depth>=3 missing kit/hierarchical chain", e.UnannotatedDepthExceedance)
	appendBucket("exceeding MaxHierarchyDepth", e.HierarchyDepthExceeded)
	appendBucket("annotated kit/passthrough under reject strictness", e.PassthroughRejected)
	return b.String()
}

// HasIssues reports whether any bucket is populated. Used by
// Root.Validate to decide whether to return a non-nil error.
func (e *ValidationError) HasIssues() bool {
	if e == nil {
		return false
	}
	return len(e.Missing)+len(e.Invalid)+
		len(e.MissingIdempotency)+len(e.InvalidIdempotency)+
		len(e.MissingShort)+len(e.MissingLong)+
		len(e.InvalidOutputSchema)+len(e.MissingStatusSubcommand)+
		len(e.MissingDryRunRationale)+len(e.MissingDestructiveToken)+
		len(e.MissingExamples)+len(e.MissingNextSteps)+
		len(e.UnannotatedTopLevelLeaf)+len(e.TooManyTopLevelVerbs)+
		len(e.UnannotatedDepthExceedance)+len(e.HierarchyDepthExceeded)+
		len(e.PassthroughRejected) > 0
}

// AsCLIError converts the validation error into a kit-style error
// envelope so RunE / Execute paths surface a uniform USAGE code and
// exit code 2.
func (e *ValidationError) AsCLIError() *output.Error {
	if e == nil || !e.HasIssues() {
		return nil
	}
	return &output.Error{
		Code:     output.CodeUsage,
		Message:  e.Error(),
		ExitCode: 2,
	}
}
