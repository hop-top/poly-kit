// Package conformance is the kit-shipped Layer-A test helper that
// adopters import as `kitconformance` to assert their CLI root
// satisfies the Layer-A contract.
//
// This is the package adopters want. Do not confuse it with
// hop.top/kit/go/console/cli/conformance, which is the cobra command
// tree behind the `kit conformance` subcommand: that package exports
// Cmd() and exit-code sentinels, not AssertCLI.
//
// The contract asserted here:
//
//   - kit/side-effect + kit/idempotent on every runnable leaf
//   - Short / Long discipline (§2 hard tier)
//   - kit/output-schema validity when declared
//   - reserved `<tool> status` subcommand present
//   - noun-verb shape with explicit annotations for top-level
//     verbs / hierarchical groupings
//   - configurable gates (EnforceDryRunRationale,
//     EnforceDestructiveToken, EnforceGuidance) when adopters wire
//     them up
//   - signature checks (local-globals, reserved-name,
//     depth-hierarchical, passthrough) — the same
//     Root.ValidateSignature walk Execute() dispatches at boot
//
// Usage from an adopter test:
//
//	import kitconformance "hop.top/kit/go/conformance"
//
//	func TestCLIConforms(t *testing.T) {
//	    root := buildRoot()
//	    kitconformance.AssertCLI(t, root)
//	}
//
// AssertCLI forces EnforceValidate=true for the duration of the
// check; the adopter's runtime configuration is not modified — the
// helper restores the previous value after Validate returns. Adopters
// who want to scope the check tighter can call AssertCLIWithOptions
// to flip the configurable Enforce* gates one by one.
package conformance

import (
	"errors"

	"hop.top/kit/go/console/cli"
)

// Options tunes the assertion. Zero value passes the check the
// AssertCLI default does: EnforceValidate=true, configurable gates
// untouched (left at their adopter-provided values).
type Options struct {
	// EnforceDryRunRationale, when true, also runs the configurable
	// dry-run-rationale gate.
	EnforceDryRunRationale bool
	// EnforceDestructiveToken, when true, also runs the
	// destructive-token gate.
	EnforceDestructiveToken bool
	// EnforceGuidance, when true, also runs the
	// examples + next-steps gate.
	EnforceGuidance bool
}

// TB is the subset of testing.TB AssertCLI uses. Adopter tests pass
// the *testing.T from their Test* func; kit's own conformance tests
// pass a stub that records failures without escalating, which lets
// the sad-path tests assert on the bucket shape that AssertCLI
// surfaces.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// AssertCLI runs both validators Execute() dispatches at boot:
// Root.Validate (with EnforceValidate forced to true) and
// Root.ValidateSignature (local-globals, reserved-name,
// depth-hierarchical, passthrough). Any Validate error or signature
// violation is reported as a failure on t — signature violations
// fail regardless of severity, matching reject-mode runtime
// behavior, which aborts on any violation without filtering. The
// adopter's Config is restored after the call returns so subsequent
// invocations of root.Execute() see the original flag values.
//
// Returns the *cli.ValidationError when Validate fails so callers
// who want to inspect individual buckets (e.g. to assert a single
// bucket-shape) can do so. Returns nil when Validate passes (even
// if signature violations were reported).
func AssertCLI(t TB, root *cli.Root) *cli.ValidationError {
	t.Helper()
	return AssertCLIWithOptions(t, root, Options{})
}

// AssertCLIWithOptions is the same as AssertCLI but additionally
// flips the configurable gates per the Options struct. Useful when
// kit's CI matrix wants to gate adoption of the new annotation
// surfaces piecewise.
func AssertCLIWithOptions(t TB, root *cli.Root, opts Options) *cli.ValidationError {
	t.Helper()
	if root == nil {
		t.Fatalf("kitconformance.AssertCLI: root is nil")
		return nil
	}
	saved := root.Config
	defer func() { root.Config = saved }()

	root.Config.EnforceValidate = true
	if opts.EnforceDryRunRationale {
		root.Config.EnforceDryRunRationale = true
	}
	if opts.EnforceDestructiveToken {
		root.Config.EnforceDestructiveToken = true
	}
	if opts.EnforceGuidance {
		root.Config.EnforceGuidance = true
	}

	// Signature validator: same walk Execute() dispatches at boot.
	// Reject mode aborts on HasViolations without severity
	// filtering, so every violation is a failure here too.
	if report := root.ValidateSignature(); report.HasViolations() {
		for _, v := range report.Violations {
			t.Errorf("kitconformance.AssertCLI: signature violation: %s [%s/%s] %s",
				v.Path, v.Check, v.Severity, v.Detail)
		}
	}

	err := root.Validate()
	if err == nil {
		return nil
	}
	var ve *cli.ValidationError
	if errors.As(err, &ve) {
		t.Errorf("kitconformance.AssertCLI: validation failed: %s", ve.Error())
		return ve
	}
	// Non-ValidationError (e.g. ConfigArgs parse failure): surface
	// the raw error message; no bucket detail available.
	t.Errorf("kitconformance.AssertCLI: %v", err)
	return nil
}
