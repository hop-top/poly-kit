package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// Annotation keys reserved under the kit/ prefix per §3.5 of
// cli-conventions-with-kit.md for evolution / capability negotiation
// (§13). All are opt-in; absence means "always present, never
// deprecated".
const (
	// kitDeprecatedSince is the schema version at which a command was
	// marked deprecated. Pairs with cobra.Command.Deprecated for the
	// human message.
	kitDeprecatedSince = "kit/deprecated-since"
	// kitRemovalTarget is the schema version in which a deprecated
	// command will be removed (purely informational).
	kitRemovalTarget = "kit/removal-target"
	// kitSince is the schema version in which a command first
	// appeared. Read by --api-version filtering: commands newer than
	// the requested version are hidden.
	kitSince = "kit/since"
	// kitFlagSince annotates a flag's introduction version. Format:
	// "<flag>=<MAJOR.MINOR>[,<flag2>=...]". Read by --api-version
	// filtering: refusing newer flags under compatibility mode.
	kitFlagSince = "kit/flag-since"
	// kitMinAPIVersion annotates the root with the oldest supported
	// API version. --api-version below this is rejected with
	// UNSUPPORTED_API_VERSION.
	kitMinAPIVersion = "kit/min-api-version"
	// API: framework — cobra annotation key for declared positional args.
	//
	// kitArgs is a comma-separated list of positional argument names
	// for manifest emission. Adopters set this on their cobra
	// commands; the spec subcommand projects it into the Manifest.
	//
	//lint:ignore U1000 framework API surface (ADR-0031)
	kitArgs = "kit/args"
	// API: framework — cobra annotation key for exit-code enumeration.
	//
	// kitExitCodes is a comma-separated list of exit-code symbols a
	// command may produce, for manifest emission. Adopters set this
	// on their cobra commands; the spec subcommand projects it into
	// the Manifest.
	//
	//lint:ignore U1000 framework API surface (ADR-0031)
	kitExitCodes = "kit/exit-codes"
	// kitSpecCommandAnnotation marks the spec subcommand itself so
	// the deprecation-warning middleware knows to skip it (warnings
	// would corrupt the manifest output).
	kitSpecCommandAnnotation = "kit/spec-command"
)

// apiVersionFlag is the global flag name for capability negotiation
// (§13). When set, commands annotated kit/since:<ver> newer than the
// requested version are hidden, and flags annotated kit/flag-since
// newer than the requested version are refused.
const apiVersionFlag = "api-version"

// CodeUnsupportedAPIVersion is the structured-error code returned when
// --api-version is older than the tool's kit/min-api-version. Maps to
// exit code 2 (USAGE) so unsupported negotiation is treated as a
// caller-side mistake.
const CodeUnsupportedAPIVersion = "UNSUPPORTED_API_VERSION"

// DeprecationWarning is the structured envelope emitted to stderr when
// an adopter invokes a deprecated command. Mirrors output.Error's shape
// so JSON consumers can parse warnings the same way they parse errors.
type DeprecationWarning struct {
	Code    string `json:"code" yaml:"code"`
	Message string `json:"message" yaml:"message"`
	Since   string `json:"since,omitempty" yaml:"since,omitempty"`
	Removal string `json:"removal,omitempty" yaml:"removal,omitempty"`
}

// CodeDeprecation is the warning code emitted for deprecation notices.
const CodeDeprecation = "DEPRECATION"

// emitDeprecationWarning writes a DEPRECATION envelope to cmd's stderr
// when cmd is annotated as deprecated. Skipped for the spec subcommand
// (which would corrupt the manifest output) and when the command is
// not deprecated.
//
// JSON/YAML format wraps the warning under a top-level "warning" key
// so it's distinguishable from the error envelope (output.Error which
// is rendered at the top level). Plaintext mode prints
// "DEPRECATION: <msg> (since <ver>, removal <ver>)" to stderr.
func emitDeprecationWarning(cmd *cobra.Command) {
	if cmd == nil || cmd.Annotations == nil {
		return
	}
	// Skip the spec subcommand itself: emitting on `<tool> spec` would
	// pollute the manifest output stream consumed by agents.
	if cmd.Annotations[kitSpecCommandAnnotation] == "true" {
		return
	}
	if cmd.Deprecated == "" &&
		cmd.Annotations[kitDeprecatedSince] == "" &&
		cmd.Annotations[kitRemovalTarget] == "" {
		return
	}

	w := DeprecationWarning{
		Code:    CodeDeprecation,
		Message: cmd.Deprecated,
		Since:   cmd.Annotations[kitDeprecatedSince],
		Removal: cmd.Annotations[kitRemovalTarget],
	}
	if w.Message == "" {
		// Cobra's deprecated message is the canonical string; fall
		// back to a generic notice if the adopter only set the
		// annotations.
		w.Message = cmd.CommandPath() + " is deprecated"
	}

	format := activeFormat(cmd)
	stderr := cmd.ErrOrStderr()
	switch format {
	case "json":
		enc := json.NewEncoder(stderr)
		enc.SetIndent("", "  ")
		_ = enc.Encode(struct {
			Warning DeprecationWarning `json:"warning"`
		}{Warning: w})
	case "yaml":
		// YAML uses the same envelope shape; tabs/spaces consistent
		// with json.Encoder's output for parity.
		fmt.Fprintf(stderr, "warning:\n  code: %s\n  message: %q\n",
			w.Code, w.Message)
		if w.Since != "" {
			fmt.Fprintf(stderr, "  since: %q\n", w.Since)
		}
		if w.Removal != "" {
			fmt.Fprintf(stderr, "  removal: %q\n", w.Removal)
		}
	default:
		// Plaintext (table / "" / unknown).
		msg := fmt.Sprintf("%s: %s", w.Code, w.Message)
		if w.Since != "" {
			msg += fmt.Sprintf(" (since %s)", w.Since)
		}
		if w.Removal != "" {
			msg += fmt.Sprintf(" (removal %s)", w.Removal)
		}
		fmt.Fprintln(stderr, msg)
	}
}

// wrapDeprecationRunE wraps inner to emit a DEPRECATION warning before
// running inner when the command is deprecated. Warnings are
// informational — they never gate execution. Inserted into the
// middleware chain BETWEEN policy (outermost) and idempotency/error-
// render: policy → deprecation → idempotency → error-render → adopter.
//
// The spec subcommand opts out via the kit/spec-command annotation so
// `<tool> spec` invocations don't corrupt the manifest output.
func wrapDeprecationRunE(
	inner func(*cobra.Command, []string) error,
) func(*cobra.Command, []string) error {
	if inner == nil {
		return nil
	}
	return func(cmd *cobra.Command, args []string) error {
		emitDeprecationWarning(cmd)
		return inner(cmd, args)
	}
}
