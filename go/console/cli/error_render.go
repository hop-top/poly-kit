package cli

import (
	"errors"

	"github.com/spf13/cobra"
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
// errors are wrapped with CodeGeneric / ExitCode 1.
func toCLIError(err error) *output.Error {
	if err == nil {
		return nil
	}
	var ce asCLIError
	if errors.As(err, &ce) {
		if out := ce.AsCLIError(); out != nil {
			return out
		}
	}
	return &output.Error{
		Code:     output.CodeGeneric,
		Message:  err.Error(),
		ExitCode: 1,
	}
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
//     envelope is rendered as-is.
//   - Otherwise, the error is wrapped with Code=CodeGeneric, ExitCode=1.
//   - In JSON/YAML mode the envelope is rendered structurally; in
//     table/plaintext mode it's rendered as "Code: Message\nFix: ...".
//   - Policy refusals come back as UNAUTHORIZED (exit 5) or
//     RATE_LIMITED (exit 64).
func (r *Root) WrapRunE() {
	// Auto-apply idempotency defaults so the conditional-idempotent
	// flag installer sees adopter intent. Validate would do this
	// itself when EnforceValidate=true; do it here too so flag
	// auto-registration works in the common opt-out case.
	applyDefaultIdempotency(r.Cmd)
	installIdempotencyKeyFlag(r.Cmd)
	installConfirmTokenFlag(r.Cmd)
	r.wrapRunESubtree(r.Cmd)
}

const wrappedAnnotation = "kit.cli.runE.wrapped"

func (r *Root) wrapRunESubtree(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil || cmd.Annotations[wrappedAnnotation] != "true" {
		if cmd.RunE != nil {
			// Innermost (adopter) → error-render → idempotency →
			// deprecation-warn → policy. Deprecation sits inside
			// policy (so policy refusal short-circuits before emitting
			// a warning) but outside idempotency/error-render so the
			// warning is emitted once even when idempotency replay
			// returns a cached result.
			inner := wrapIdempotencyRunE(wrapRunE(cmd.RunE), r.IdemStore)
			inner = wrapDeprecationRunE(inner)
			cmd.RunE = r.wrapPolicyRunE(cmd, inner)
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
