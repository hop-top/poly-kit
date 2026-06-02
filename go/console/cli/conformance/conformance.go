// Package conformance provides the "kit conformance" CLI subcommand
// tree for static checks, integration harness, scenario-leak gates,
// and hook installation. Children are introduced incrementally by the
// 12fcc track family; names not yet implemented are reserved so the
// help tree is honest about the eventual surface.
//
// Subcommands:
//
//	kit conformance verify-no-leak    [--staged|--diff=<spec>|--audit|--paths=...] [--format json|human]
//	kit conformance install-hooks     [--dry-run] [--force] [--format json|human]
//	kit conformance verify-stories    [--paths=...] [--strict-toolspec] [--format json|human]
//	kit conformance static            (reserved placeholder)
//	kit conformance harness           (reserved placeholder)
//	kit conformance generate-stories  (reserved placeholder)
//
// Exit codes (full contract enforced by leaf RunE):
//
//	0 clean         no findings
//	2 leak_detected one or more scenario-shaped blocks found
//	3 usage_error   bad flags, or "not yet implemented" reserved name
//	4 io_error      git/gh failed; retryable
//	5 config_error  bad .verifynoleak.allow or bare-ignore-rejected
package conformance

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/conformance/badge"
	"hop.top/kit/go/console/cli/conformance/grade"
	svccmd "hop.top/kit/go/console/cli/conformance/svc"
	"hop.top/kit/go/console/output"
)

// Cmd returns the top-level "conformance" command with all leaf
// subcommands attached, including reserved placeholders.
func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "conformance",
		Aliases: []string{"con"},
		Short:   "Run kit 12-factor conformance checks",
		Long: `Static, structural, behavioral, and leak conformance
gates for kit apps. Each subcommand corresponds to a layer of the
12-factor CLI conformance contract.

The alias "con" is available for terser invocation
(e.g. "kit con verify-no-leak --staged").`,
		Args: cobra.NoArgs,
	}
	verify := verifyNoLeakCmd()
	install := installHooksCmd()
	stories := verifyStoriesCmd()
	gradeCmd := grade.Cmd()
	badgeCmd := badge.Cmd()
	static := reservedCmd("static", "12fcc-static")
	harness := reservedCmd("harness", "12fcc-harness")
	generateStories := reservedCmd("generate-stories", "12fcc-storygen")
	svc := svccmd.Cmd()
	// Kit-internal conformance leaves are exempt from Layer-A
	// registration validation. gradeCmd + badgeCmd carry the full
	// annotation set (side-effect, idempotent, examples, next-steps)
	// and do not need the exemption.
	for _, c := range []*cobra.Command{verify, install, stories, static, harness, generateStories, svc} {
		cli.SetExemptValidation(c)
	}
	cmd.AddCommand(verify, install, stories, gradeCmd, badgeCmd, static, harness, generateStories, svc)
	return cmd
}

// Conformance exit-code codes. These are conformance-tree-local and
// extend output.Code* — they coexist with kit-wide codes by reusing
// the same numeric exit slots: e.g. leak_detected reuses slot 2 even
// though kit-wide that's also the CodeUsage slot. The Code string is
// what disambiguates for JSON consumers; the ExitCode is what
// disambiguates for shells.
const (
	CodeLeakDetected = "LEAK_DETECTED" // exit 2
	CodeUsage        = "USAGE"         // exit 3 (overrides kit-wide CodeNotFound for the conformance tree)
	CodeIO           = "IO"            // exit 4
	CodeConfig       = "CONFIG"        // exit 5
)

// Sentinel errors used across conformance subcommands. Each is a
// typed *output.Error implementing AsCLIError() so kit's RunE
// middleware preserves the exit code through fang.Execute. main()
// reads envelope.ExitCode for process-exit.
//
// Constructors below (LeakDetectedError, UsageError, IOError,
// ConfigError) build sentinels with custom messages while keeping
// errors.Is(err, ErrX) true for switch-friendly testing.
var (
	// ErrLeakDetected is the identity sentinel for any verify-no-leak
	// finding. Compare with errors.Is. Exit code 2.
	ErrLeakDetected = &conformanceSentinel{code: CodeLeakDetected, exit: 2, msg: "verify-no-leak: scenario-shaped content detected"}

	// ErrUsage is the identity sentinel for bad flags or invocations
	// of reserved-but-unimplemented subcommands. Exit code 3.
	ErrUsage = &conformanceSentinel{code: CodeUsage, exit: 3, msg: "conformance: usage error"}

	// ErrIO is the identity sentinel for git/gh failures the caller
	// should retry. Exit code 4.
	ErrIO = &conformanceSentinel{code: CodeIO, exit: 4, msg: "conformance: io error"}

	// ErrConfig is the identity sentinel for malformed
	// .verifynoleak.allow or a bare ignore comment missing its reason.
	// Exit code 5.
	ErrConfig = &conformanceSentinel{code: CodeConfig, exit: 5, msg: "conformance: config error"}
)

// conformanceSentinel is the typed error backing the package's
// sentinels. It satisfies error + AsCLIError so kit's RunE middleware
// renders the right envelope and main() exits with the right code.
//
// To attach context (file path, rule id, etc.) without losing
// errors.Is identity, use UsageError(msg) / LeakDetectedError(msg) /
// etc. which return a wrapped form that still chain-matches the
// identity sentinel.
type conformanceSentinel struct {
	code string
	exit int
	msg  string
}

func (s *conformanceSentinel) Error() string { return s.msg }

func (s *conformanceSentinel) AsCLIError() *output.Error {
	return &output.Error{Code: s.code, Message: s.msg, ExitCode: s.exit}
}

// wrappedSentinel decorates a base conformanceSentinel with a
// custom message + optional cause + suggested fix while preserving
// errors.Is identity through Unwrap.
type wrappedSentinel struct {
	base    *conformanceSentinel
	message string
	cause   string
	fix     string
}

func (w *wrappedSentinel) Error() string {
	if w.message == "" {
		return w.base.msg
	}
	return w.base.msg + ": " + w.message
}

func (w *wrappedSentinel) Unwrap() error { return w.base }

func (w *wrappedSentinel) AsCLIError() *output.Error {
	return &output.Error{
		Code:         w.base.code,
		Message:      w.Error(),
		Cause:        w.cause,
		SuggestedFix: w.fix,
		ExitCode:     w.base.exit,
	}
}

// UsageError returns a wrapped ErrUsage with the given detail.
// errors.Is(err, ErrUsage) remains true.
func UsageError(detail string) error {
	return &wrappedSentinel{base: ErrUsage, message: detail}
}

// LeakDetectedError returns a wrapped ErrLeakDetected. The detail
// typically summarizes the finding count or file. Per-finding output
// is rendered separately to stdout/stderr by the verify-no-leak
// formatter — this envelope is the exit-code carrier.
func LeakDetectedError(detail string) error {
	return &wrappedSentinel{base: ErrLeakDetected, message: detail}
}

// IOError returns a wrapped ErrIO. cause is the underlying command
// output (e.g. "git: not a repository"); fix nudges the operator.
func IOError(detail, cause, fix string) error {
	return &wrappedSentinel{base: ErrIO, message: detail, cause: cause, fix: fix}
}

// ConfigError returns a wrapped ErrConfig with the file path and
// line where the misconfiguration was detected.
func ConfigError(detail, cause, fix string) error {
	return &wrappedSentinel{base: ErrConfig, message: detail, cause: cause, fix: fix}
}

// ExitCode classifies known sentinel errors. Returns (code, true) on
// match, (0, false) otherwise. Kept for direct testing; runtime exit
// resolution now flows through envelope.ExitCode in kit's main().
func ExitCode(err error) (int, bool) {
	switch {
	case err == nil:
		return 0, true
	case errors.Is(err, ErrLeakDetected):
		return 2, true
	case errors.Is(err, ErrUsage):
		return 3, true
	case errors.Is(err, ErrIO):
		return 4, true
	case errors.Is(err, ErrConfig):
		return 5, true
	}
	return 0, false
}

// reservedCmd returns a placeholder subcommand for names owned by a
// sibling track that has not yet implemented its conformance layer.
// Invoking the placeholder exits 3 (usage_error) with a pointer to
// the owning track.
func reservedCmd(name, track string) *cobra.Command {
	return &cobra.Command{
		Use:    name,
		Short:  fmt.Sprintf("Reserved for %s (not yet implemented)", track),
		Hidden: false,
		Args:   cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return UsageError(fmt.Sprintf("%q is reserved for track %s and is not yet implemented in this kit version", name, track))
		},
	}
}
