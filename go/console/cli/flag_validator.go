// Flag-value validator middleware for kit CLIs.
//
// Adopters often need to reject ill-formed persistent flag values
// (e.g. `--api-version=foo`) before any leaf RunE dispatches, and
// have the rejection rendered through the same structured envelope
// the rest of the tool uses for errors. Hand-rolling a tree-walking
// installer at adopter level couples them to cobra internals and
// bypasses kit's error renderer; this middleware centralizes both.
//
// Layering: cobra parses the flag → kit walks leaves at WrapRunE
// time → for every leaf, validators run BEFORE the adopter RunE →
// a non-nil *output.Error from a validator bubbles up through
// wrapRunE, which routes it through output.RenderError so JSON/YAML
// callers see the envelope.
//
// Ordering constraint: register validators BEFORE calling
// Root.WrapRunE (or before Root.Execute, which calls it). Validators
// registered afterwards are inert — the leaves are already wrapped.
//
// Example:
//
//	root.WithFlagValidator("api-version", func(v string) *output.Error {
//	    if !semver.IsValid(v) {
//	        return &output.Error{
//	            Code:     "INVALID_API_VERSION",
//	            Message:  "api-version must be semver",
//	            ExitCode: 2,
//	        }
//	    }
//	    return nil
//	})
//	root.WrapRunE() // installs the validator alongside the renderer
package cli

import (
	"github.com/spf13/cobra"
	"hop.top/kit/go/console/output"
)

// FlagValidator inspects a parsed flag value and returns nil to
// accept or a structured *output.Error to reject. The rejection is
// routed through the kit error renderer so the user-visible output
// honors --format json|yaml|table|text.
type FlagValidator func(value string) *output.Error

// WithFlagValidator registers a validator for a persistent flag by
// name. The validator fires once per leaf invocation, AFTER cobra
// parses the flag and BEFORE the adopter's RunE — but only when the
// user actually set the flag (cobra's flag.Changed). Defaults are
// not validated: adopters opt into "validate the default" by
// inspecting it themselves at construction time.
//
// A non-nil *output.Error return is rendered through wrapRunE's
// error-envelope middleware, so JSON/YAML/CSV/text callers all see
// the structured envelope rather than a bare stderr line.
//
// Ordering: call WithFlagValidator BEFORE WrapRunE (or before
// Execute, which calls WrapRunE). Validators registered after the
// subtree is wrapped never fire — the closures captured at wrap
// time are immutable.
//
// Calling WithFlagValidator twice with the same name overwrites
// the earlier registration (last wins) — keeps test setup
// ergonomic.
//
// Registering with a flag name that doesn't exist anywhere on the
// command tree is a programmer error. WithFlagValidator does NOT
// panic; the validator silently never fires because cmd.Flag(name)
// returns nil at run time. Adopters who want strict registration
// can check r.Cmd.PersistentFlags().Lookup(name) themselves.
func (r *Root) WithFlagValidator(name string, fn FlagValidator) *Root {
	if r == nil || name == "" || fn == nil {
		return r
	}
	if r.flagValidators == nil {
		r.flagValidators = make(map[string]FlagValidator)
	}
	r.flagValidators[name] = fn
	return r
}

// wrapFlagValidatorRunE wraps orig so registered flag validators
// run before orig. Returned as the innermost layer so wrapRunE's
// error-envelope middleware sees the validator's *output.Error and
// renders it like any other adopter error.
//
// Empty validator map → orig unchanged (zero overhead on tools
// that don't use this feature).
func wrapFlagValidatorRunE(
	orig func(*cobra.Command, []string) error,
	validators map[string]FlagValidator,
) func(*cobra.Command, []string) error {
	if orig == nil || len(validators) == 0 {
		return orig
	}
	return func(cmd *cobra.Command, args []string) error {
		for name, fn := range validators {
			flag := cmd.Flag(name)
			if flag == nil {
				// Flag isn't visible on this leaf. Could be a
				// programmer error (unknown name) or a flag that
				// genuinely isn't inherited here. Silent skip is
				// safer than panicking inside the dispatch path.
				continue
			}
			if !flag.Changed {
				// User didn't set the flag — leave default values
				// alone. Adopters who want to validate defaults can
				// do so eagerly at construction time.
				continue
			}
			if e := fn(flag.Value.String()); e != nil {
				return e
			}
		}
		return orig(cmd, args)
	}
}
