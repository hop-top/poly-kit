package cli

import (
	"strings"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/output"
)

// correctArgs returns argv with the mistyped flag token rewritten, and
// reports whether anything changed.
//
// Only the token that names the flag is touched, and only in the forms
// pflag itself accepts: `--typed`, `--typed=value`. A `--typed value`
// pair leaves the value alone as a separate element, which is what
// rewriting only the name achieves.
//
// A bare `--` ends flag parsing, so nothing after it is a flag however
// much it looks like one. Rewriting past it would corrupt a positional
// the command deliberately accepts verbatim.
func correctArgs(args []string, typed, corrected string) ([]string, bool) {
	out := make([]string, len(args))
	copy(out, args)
	changed := false
	for i, a := range out {
		if a == "--" {
			break
		}
		if a == "--"+typed {
			out[i] = "--" + corrected
			changed = true
			continue
		}
		if strings.HasPrefix(a, "--"+typed+"=") {
			out[i] = "--" + corrected + strings.TrimPrefix(a, "--"+typed)
			changed = true
		}
	}
	return out, changed
}

// correctionNotice is the structured record of an applied rewrite.
//
// Shaped as an *output.Error and rendered through output.RenderError so a
// --format json|yaml caller parses it with the same decoder it already
// uses for the suggest-only envelope — one shape for this seam, whether
// the correction was applied or merely offered. Code is OK and ExitCode 0
// because nothing failed: the envelope reports what kit did, and the
// run's own outcome decides the exit status.
//
// On stderr, like every other envelope kit writes, so a caller consuming
// the command's data on stdout is unaffected by the notice appearing.
func correctionNotice(typed, corrected string) *output.Error {
	return &output.Error{
		Code:    output.CodeOK,
		Message: "applied flag correction: --" + typed + " -> --" + corrected,
		// The half an audit record cannot reconstruct from the corrected
		// argv alone. Non-negotiable 4.
		CorrectedFrom: "--" + typed,
		SuggestedFix:  "--" + corrected,
		ExitCode:      0,
		Transience:    output.TransiencePermanent,
	}
}

// applyPendingCorrection re-dispatches the invocation on the corrected
// argv, returning the corrected run's own error and whether a
// re-dispatch happened at all.
//
// The rewritten argv goes back through the SAME path the original took —
// tree reset, flag parse, PersistentPreRunE, and the whole WrapRunE
// chain. Nothing is short-circuited, which is non-negotiable 5: the
// dry-run gate, the confirm gate, the flag validators and the
// idempotency middleware all see the corrected flags because they are
// reached the only way they are ever reached. A hand-rolled "run the
// leaf directly" shortcut would have skipped every one of them, and a
// --dry-run that silently applied to the typed flags rather than the
// ones that ran is precisely the audit failure this feature must not
// introduce.
//
// The notice is written BEFORE the re-dispatch so its ordering on stderr
// is deterministic — ahead of whatever the corrected run itself writes,
// rather than interleaved with it.
func (r *Root) applyPendingCorrection(exec func(args []string) error) (error, bool) {
	p := r.pending
	if p == nil {
		return nil, false
	}
	// Clear before running. The corrected run parses flags again, and a
	// second unknown flag in the same argv would otherwise capture a
	// fresh correction and recurse: one rewrite per invocation, always.
	r.pending = nil

	corrected, changed := correctArgs(r.resolveArgs(), p.typed, p.corrected)
	if !changed {
		// The token is not in argv in a form that can be rewritten (a
		// shorthand cluster, or a value that merely resembled the name).
		// Suggest-only stands, and it has already been rendered.
		return nil, false
	}

	notice := correctionNotice(p.typed, p.corrected)
	target := p.cmd
	if target == nil {
		target = r.Cmd
	}
	_ = output.RenderError(target.ErrOrStderr(), activeFormat(target), notice)

	// Cobra parsed the failed argv onto this tree and left every value
	// and Changed bit behind; the corrected parse has to start from the
	// defaults or it inherits them. Execute's own resetForExecute did
	// this for the first parse.
	ResetFlags(r.Cmd)
	// Silencing survives from the failed parse (usageError set both on
	// the resolved command). A corrected run that fails must render its
	// own failure, so hand the flags back before dispatching.
	restoreSilence(r.Cmd)

	return exec(corrected), true
}

// restoreSilence clears the SilenceErrors/SilenceUsage the failed parse
// set on the tree, so the corrected run's own errors are rendered rather
// than swallowed.
//
// Walks the whole tree because usageError sets the pair on whichever
// command cobra had resolved, which is not necessarily the root and not
// necessarily the command the corrected argv resolves to.
func restoreSilence(root *cobra.Command) {
	walk(root, func(cmd *cobra.Command) {
		cmd.SilenceErrors = false
		cmd.SilenceUsage = false
	})
}
