package cli

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// captureCorrection records a rewrite for Execute to apply, when the
// active policy, the candidate filter, and the side-effect gate all
// agree. Called from the FlagErrorFunc chain with the error pflag
// raised, BEFORE the envelope is built.
//
// Returns true when it took ownership of this failure, meaning a
// re-dispatch is coming and the suggest-only envelope must NOT be
// rendered. Two envelopes on stderr is not one JSON document, and a
// USAGE envelope reporting exit_code 2 alongside a run that goes on to
// succeed contradicts itself.
//
// Suppression is safe precisely because the decision is complete here:
// the policy is resolved, the side-effect gate has run, the candidate is
// unique, and a prompt — if the mode called for one — has already been
// answered. Every path that does NOT return true leaves the rendering
// exactly as it was, which is what makes non-negotiable 7 hold: a
// declined prompt, a non-TTY fallback, a refused side-effect and a
// disabled policy all emit the identical suggest-only envelope, because
// none of them touched it.
//
// Ordering inside the chain matters. This runs ahead of flagParseError so
// it sees the bare *pflag.NotExistError; once flagParseError has wrapped
// it in an *output.Error the typed name is only reachable through
// errors.As, and an adopter FlagErrorFunc that built its own envelope
// would be indistinguishable from kit's.
func (r *Root) captureCorrection(cmd *cobra.Command, err error) bool {
	if r == nil || err == nil {
		return false
	}
	var notExist *pflag.NotExistError
	if !errors.As(err, &notExist) {
		// Only an unknown flag NAME is correctable. A missing value or a
		// malformed one is a different mistake with no candidate to pick
		// from, and correcting a value is explicitly out of scope.
		return false
	}
	// An adopter FlagErrorFunc that already produced an envelope owns the
	// whole rendering, same rule flagParseError follows. Rewriting argv
	// under an envelope kit did not build would apply a correction the
	// adopter may have deliberately declined to offer.
	if isEnvelopedError(err) {
		return false
	}
	if notExist.GetSpecifiedShortnames() != "" {
		// A shorthand group (-xyz) carries no name to match against, so
		// the suggester refuses it and so must the corrector.
		return false
	}

	mode := r.autocorrectMode(cmd)
	if mode == AutocorrectOff {
		return false
	}

	allowed, defaultYes := autocorrectAllowed(cmd, mode)
	if !allowed {
		return false
	}

	typed := notExist.GetSpecifiedName()
	corrected := correctionCandidate(typed, sortedFlagNames(cmd, r.hiddenDefaultFlagSet()))
	if corrected == "" {
		return false
	}

	if mode == AutocorrectPrompt {
		applied, asked := promptAutocorrect(r.promptSource, typed, corrected, defaultYes)
		if !asked {
			// No terminal to ask on. Suggest-only, and never a block:
			// this is the whole of non-negotiable 6's fallback.
			return false
		}
		if !applied {
			// Declined. The suggest-only envelope below is exactly what
			// an uncorrected run emits, and exit stays 2.
			return false
		}
	}

	r.pending = &pendingCorrection{
		typed:     typed,
		corrected: corrected,
		cmd:       cmd,
		mode:      mode,
	}
	return true
}

// hiddenDefaultFlagSet is the kit-owned plumbing flag set in the shape
// sortedFlagNames wants. Built per call rather than cached: the slice is
// a handful of names and a correction happens at most once per
// invocation.
func (r *Root) hiddenDefaultFlagSet() map[string]struct{} {
	out := make(map[string]struct{}, len(r.hiddenDefaultFlags))
	for _, name := range r.hiddenDefaultFlags {
		out[name] = struct{}{}
	}
	return out
}
