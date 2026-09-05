package cli

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"hop.top/kit/go/console/output"
)

// asCLIError is the conversion interface used by the RunE middleware.
// Adopter-defined typed errors implement this for full control over the
// rendered Code / ExitCode / Cause / SuggestedFix.
type asCLIError interface {
	AsCLIError() *output.Error
}

// toCLIError converts err to an *output.Error following the rules in the
// task spec: typed errors implementing AsCLIError() pass through; bare
// errors are wrapped with CodeGeneric / ExitCode 1. Either way the
// originating error is retained so errors.Is keeps matching sentinels
// across the conversion.
func toCLIError(err error) *output.Error {
	if err == nil {
		return nil
	}
	var ce asCLIError
	if errors.As(err, &ce) {
		if out := ce.AsCLIError(); out != nil {
			// The adopter owns every rendered field; reattach err so the
			// conversion doesn't sever errors.Is. Sentinel-bearing types
			// (e.g. conformance.UsageError) document that identity holds,
			// and that promise has to survive the envelope too.
			out = out.Retaining(err)
			if out.Transience == "" {
				// Unset (not adopter-chosen) transience defaults from
				// the code so passthrough envelopes predating the field
				// still classify on the wire. WithTransience copies, so
				// shared package-level envelopes stay untouched.
				out = out.WithTransience(output.TransienceForCode(out.Code))
			}
			return out
		}
	}
	// WrapError retains err so errors.Is still matches sentinels after the
	// middleware converts a handler failure into the envelope.
	return output.WrapError(err, output.CodeGeneric, 1)
}

// activeFormat returns the --format value visible to cmd. Empty when the
// flag isn't registered (e.g. Disable.Format = true).
func activeFormat(cmd *cobra.Command) string {
	for c := cmd; c != nil; c = c.Parent() {
		if pf := c.PersistentFlags().Lookup("format"); pf != nil {
			return pf.Value.String()
		}
		if pf := c.Flags().Lookup("format"); pf != nil {
			return pf.Value.String()
		}
	}
	return ""
}

// wrapRunE returns a RunE that runs orig and, on error, materializes an
// *output.Error envelope and writes it to cmd's stderr. The original
// error is returned so cobra/fang still see a non-nil error and the
// process exits non-zero.
func wrapRunE(orig func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	if orig == nil {
		return nil
	}
	return func(cmd *cobra.Command, args []string) error {
		err := orig(cmd, args)
		if err == nil {
			return nil
		}
		ce := toCLIError(err)
		format := activeFormat(cmd)
		// Errors go to stderr regardless of format. Data still goes
		// to stdout.
		_ = output.RenderError(cmd.ErrOrStderr(), format, ce)
		// Silence cobra/fang's own error printer so we don't double-render.
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		// Preserve sentinel-bearing wrappers (e.g. flagValidationError)
		// so outer middleware can still match via errors.Is. The render
		// already used toCLIError to extract the envelope.
		if errors.Is(err, errFlagValidation) {
			return err
		}
		return ce
	}
}

// WrapRunE walks cmd's subtree and wraps every leaf RunE with kit's
// RunE middleware chain (outer-to-inner):
//
//  1. Policy enforcement (§8.6). Runs the --confirm matrix, prompts on
//     destructive commands when needed, gates against the loaded
//     --policy, and accounts the --max-ops budget after success.
//  2. Idempotency replay (when r.IdemStore is non-nil and the
//     command is conditional-idempotent + write/destructive). Hits
//     replay recorded output; misses tee stdout into the store.
//  3. Error envelope rendering. Errors are rendered to stderr as
//     output.Error.
//
// Calling WrapRunE more than once is a no-op for already-wrapped
// commands (the wrapper is idempotent — marked by an annotation).
//
// The middleware behavior:
//   - If RunE returns nil, nothing is written to stderr.
//   - If RunE returns an error implementing AsCLIError(), the returned
//     envelope is rendered as-is; the originating error is reattached so
//     errors.Is still matches sentinels, without altering any rendered field.
//   - Otherwise, the error is wrapped with Code=CodeGeneric, ExitCode=1.
//   - In JSON/YAML mode the envelope is rendered structurally; in
//     table/plaintext mode it's rendered as "Code: Message\nFix: ...".
//   - Policy refusals come back as UNAUTHORIZED (exit 5) or
//     RATE_LIMITED (exit 64).
//   - Cobra's own validation of the invocation — positional arity,
//     unknown or malformed flags, required flags, flag groups — is
//     USAGE (exit 2). Those errors are raised before RunE, so they
//     are classified at cobra's seams (see installUsageClassification)
//     rather than in the RunE chain.
func (r *Root) WrapRunE() {
	// Auto-apply idempotency defaults so the conditional-idempotent
	// flag installer sees adopter intent. Validate would do this
	// itself when EnforceValidate=true; do it here too so flag
	// auto-registration works in the common opt-out case.
	applyDefaultIdempotency(r.Cmd)
	installIdempotencyKeyFlag(r.Cmd)
	installConfirmTokenFlag(r.Cmd)
	r.installUsageClassification()
	r.wrapRunESubtree(r.Cmd)
}

const wrappedAnnotation = "kit.cli.runE.wrapped"

func (r *Root) wrapRunESubtree(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil || cmd.Annotations[wrappedAnnotation] != "true" {
		if cmd.RunE != nil {
			// Innermost (adopter) → flag-validator → error-render →
			// idempotency → deprecation-warn → policy. Flag
			// validators sit inside error-render so their
			// *output.Error returns route through RenderError and
			// honor --format. Validators run AFTER policy/idempotency
			// only because we want the error envelope to wrap them;
			// in practice a flag-shape rejection short-circuits before
			// any meaningful adopter work runs.
			adopter := wrapFlagValidatorRunE(cmd.RunE, r.flagValidators)
			inner := wrapIdempotencyRunE(wrapRunE(adopter), r.IdemStore)
			inner = wrapDeprecationRunE(inner)
			cmd.RunE = r.wrapPolicyRunE(cmd, inner)
			cmd.PreRunE = wrapPreRunUsage(cmd)
			if cmd.Annotations == nil {
				cmd.Annotations = make(map[string]string)
			}
			cmd.Annotations[wrappedAnnotation] = "true"
		}
	}
	for _, c := range cmd.Commands() {
		r.wrapRunESubtree(c)
	}
}

// Cobra validates an invocation before it reaches RunE: pflag's parse
// errors are handed to the command's FlagErrorFunc, the Args validator
// checks positional arity, and required flags and flag groups are
// checked between PreRun and RunE. All of them leave cobra as errors
// the RunE chain never sees, so without classification they reach
// Execute bare and every surface reads them as GENERIC (exit 1).
//
// They are the caller's mistake, which the taxonomy names USAGE (exit
// 2), so kit hooks each seam and classifies there. No message matching
// is involved: pflag's errors are typed, and an error returned by an
// Args validator or by cobra's own ValidateRequiredFlags and
// ValidateFlagGroups is a validation failure by construction. The
// originating error is retained, so errors.As still reaches the pflag
// type for a caller that wants the offending flag.
const (
	usageFlagHookAnnotation = "kit.cli.usage.flagErrorFunc"
	usageArgsHookAnnotation = "kit.cli.usage.args"
)

// installUsageClassification hooks the FlagErrorFunc on the root and
// wraps the Args validator of every command in the tree. Idempotent:
// each hook is recorded in an annotation and installed once.
//
// A FlagErrorFunc the adopter installed still runs first; kit
// classifies whatever it returns bare. A command whose Args is nil is
// left alone: cobra's ArbitraryArgs never fails, and a nil Args is
// also what makes cobra's Find fall back to its legacy unknown-command
// check, which a wrapper would suppress.
func (r *Root) installUsageClassification() {
	root := r.Cmd
	if root.Annotations == nil || root.Annotations[usageFlagHookAnnotation] != "true" {
		prev := root.FlagErrorFunc()
		root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
			return usageError(cmd, prev(cmd, err))
		})
		annotate(root, usageFlagHookAnnotation)
	}
	walk(root, func(cmd *cobra.Command) {
		if cmd.Args == nil {
			return
		}
		if cmd.Annotations != nil && cmd.Annotations[usageArgsHookAnnotation] == "true" {
			return
		}
		orig := cmd.Args
		cmd.Args = func(cmd *cobra.Command, args []string) error {
			return usageError(cmd, orig(cmd, args))
		}
		annotate(cmd, usageArgsHookAnnotation)
	})
}

// wrapPreRunUsage returns a PreRunE that runs cobra's required-flag
// and flag-group checks ahead of the adopter's pre-run hook and
// classifies a failure as USAGE. Cobra runs the same two checks itself
// right after PreRun and returns their errors bare, with no seam to
// hook; running them one step earlier is the only typed way to reach
// them. A hook that fails here fails the same invocation cobra would
// have failed, one hook sooner.
//
// The adopter's PreRunE, or PreRun when that is what they set, runs
// afterwards exactly as cobra would have run it.
func wrapPreRunUsage(cmd *cobra.Command) func(*cobra.Command, []string) error {
	preRunE, preRun := cmd.PreRunE, cmd.PreRun
	return func(c *cobra.Command, args []string) error {
		if err := c.ValidateRequiredFlags(); err != nil {
			return usageError(c, err)
		}
		if err := c.ValidateFlagGroups(); err != nil {
			return usageError(c, err)
		}
		if preRunE != nil {
			return preRunE(c, args)
		}
		if preRun != nil {
			preRun(c, args)
		}
		return nil
	}
}

// usageError classifies err, raised by cobra while validating the
// invocation of cmd, as USAGE and renders it the way wrapRunE renders
// a handler failure, so the caller sees one envelope shape whichever
// layer refused. nil and help requests pass through untouched, as does
// an error that already carries a kit envelope: an adopter's own
// validator that chose its code keeps it.
func usageError(cmd *cobra.Command, err error) error {
	if err == nil || errors.Is(err, pflag.ErrHelp) {
		return err
	}
	var ce asCLIError
	if errors.As(err, &ce) {
		return err
	}
	out := output.WrapError(err, output.CodeUsage, int(ExitUsage))
	out.SuggestedFix = "run '" + cmd.CommandPath() + " --help' for usage"
	_ = output.RenderError(cmd.ErrOrStderr(), activeFormat(cmd), out)
	// Silence cobra's own printer so the envelope is not doubled.
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	return out
}

// annotate records a kit-owned marker on cmd.
func annotate(cmd *cobra.Command, key string) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[key] = "true"
}
