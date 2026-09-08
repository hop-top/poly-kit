package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// autocorrectMaxDistance caps how far a typed name may stray from a real
// flag before kit will REWRITE it, as opposed to merely suggesting it.
//
// Deliberately tighter than flagErrorMaxDistance (2), which governs
// suggestion. The two thresholds answer different questions. A suggestion
// is read by a human or an agent that then decides, so a slightly wrong
// candidate costs a glance. A rewrite is the decision, so the bar is the
// distance at which a single edit explains the whole difference: one
// dropped, doubled, wrong, or transposed character. At distance 2 the
// nearest flag is frequently not the intended one, and there is no second
// reader to catch it.
//
// Changing this does not change suggested_fix or alternatives; those keep
// reading flagErrorMaxDistance.
const autocorrectMaxDistance = 1

// pendingCorrection records a flag rewrite the FlagErrorFunc decided is
// safe to apply, for Execute to act on after the failed parse unwinds.
//
// Captured at the parse seam and applied at Execute because neither place
// can do both halves. Only the FlagErrorFunc knows, from pflag's typed
// error, which token was rejected and which command cobra had resolved;
// only Execute owns the argv and can hand a rewritten one back to cobra.
type pendingCorrection struct {
	// typed is the flag name as the caller wrote it, without dashes.
	typed string
	// corrected is the real flag name that replaces it, without dashes.
	corrected string
	// cmd is the leaf cobra had resolved when the parse failed. Held so
	// the side-effect gate and the prompt's default answer read the same
	// command the rewritten argv will dispatch to.
	cmd *cobra.Command
	// mode is the policy that authorized the capture, so Execute does
	// not re-resolve it against a tree whose flags have since been
	// reset.
	mode AutocorrectMode
}

// correctionCandidate returns the single flag name that may replace a
// mistyped one, or "" when nothing qualifies.
//
// Three filters, all of which must hold. Each exists because dropping it
// turns a correction into a guess:
//
//  1. Above the length floor. Below flagSuggestMinLength every candidate
//     is within one edit of half a real flag table; the suggester already
//     refuses to name a winner there, and a rewrite asserts more
//     certainty than a suggestion, not less.
//  2. Exactly one candidate. A tie is the case where kit demonstrably
//     does not know which flag was meant, so there is nothing to apply.
//  3. Distance <=1, or an exact prefix. A unique prefix is an
//     abbreviation the caller was in the middle of typing, which is a
//     stronger signal than any edit distance; otherwise one edit is the
//     ceiling (see autocorrectMaxDistance).
//
// The candidate list is suggestFlags' own, so a correction is always a
// flag the suggester would have offered. This selects from that list, it
// does not widen it.
func correctionCandidate(typed string, candidates []string) string {
	if len(typed) < flagSuggestMinLength {
		return ""
	}
	matches := suggestFlags(typed, candidates)
	if len(matches) != 1 {
		return ""
	}
	hit := matches[0]
	lowerTyped := strings.ToLower(typed)
	if strings.HasPrefix(strings.ToLower(hit), lowerTyped) {
		// suggestFlags returns prefix matches in preference to distance
		// matches, so a single prefixed hit is already unique among
		// prefixes. An abbreviation needs no distance ceiling.
		return hit
	}
	if levenshtein(lowerTyped, strings.ToLower(hit)) <= autocorrectMaxDistance {
		return hit
	}
	return ""
}

// autocorrectAllowed reports whether the resolved leaf may have a flag
// rewritten under mode, and what the prompt's default answer should be.
//
// The side-effect gate, non-negotiable 2 of this feature. Auto-applying
// requires kit/side-effect: read and nothing else: a leaf that mutates
// state, a leaf that destroys it, an interactive leaf, and a leaf that
// declared no side-effect at all are all refused in read mode.
//
// An UNANNOTATED leaf is refused deliberately. IsReadOnly returns false
// for a missing annotation, and that is the conservative reading kit
// wants here — absence of a declaration is not a declaration of safety.
//
// In prompt mode a mutating leaf may still be corrected, but only after
// an explicit y: defaultYes is false there, so a bare Enter declines.
// Reads default to yes, which is what makes the mode worth having for
// the `list --stauts` case it exists to serve.
func autocorrectAllowed(cmd *cobra.Command, mode AutocorrectMode) (allowed, defaultYes bool) {
	readOnly := IsReadOnly(cmd)
	switch mode {
	case AutocorrectRead:
		// No prompt in this mode; defaultYes is meaningless.
		return readOnly, false
	case AutocorrectPrompt:
		// Every leaf may be asked about, but only a read-only one gets
		// the convenient default. Enter on a destructive verb must
		// decline, not proceed.
		return true, readOnly
	}
	return false, false
}
