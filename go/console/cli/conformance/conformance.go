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
//	kit conformance harness record    --scenario <yaml> --binary <path> --out <dir>
//	kit conformance static            (reserved placeholder)
//	kit conformance generate-stories  (reserved placeholder)
//
// Exit codes (full contract enforced by leaf RunE):
//
//	 0 clean          no findings
//	 2 usage          bad flags, or "not yet implemented" reserved name
//	 6 io             git/gh/fs environment failed; transient, retry may clear
//	66 leak_detected  one or more scenario-shaped blocks found
//	67 config         bad .verifynoleak.allow or bare-ignore-rejected
//
// The contract follows the reconciled 12fc taxonomy (0 success,
// 1 general, 2 usage, 3 not-found, 4 conflict, 5 permission/auth,
// 6 transient/retryable, >6 documented per-tool): usage sits on the
// shared slot 2; io failures are exactly the transient class CI
// excludes so a flaky network cannot red-light a build; leak and
// config findings are tool-specific verdicts with no shared class,
// so they live in kit's documented >6 band, contiguous with
// RATE_LIMITED (64) and PROVENANCE_MISSING (65) from
// go/console/output. Earlier kit versions used 2 leak / 3 usage /
// 4 io / 5 config, which collided with the shared meanings of 2-5;
// pin a kit release if you still consume the old numbers.
package conformance

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/conformance/badge"
	"hop.top/kit/go/console/cli/conformance/grade"
	harnessrecord "hop.top/kit/go/console/cli/conformance/harness/record"
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
	harness := harnessrecord.Group()
	generateStories := reservedCmd("generate-stories", "12fcc-storygen")
	svc := svccmd.Cmd()
	// Kit-internal conformance leaves are exempt from Layer-A
	// registration validation. gradeCmd + badgeCmd carry the full
	// annotation set (side-effect, idempotent, examples, next-steps)
	// and do not need the exemption; the harness record leaf carries
	// its own full annotation set inside harnessrecord.Group.
	for _, c := range []*cobra.Command{verify, install, stories, static, generateStories, svc} {
		cli.SetExemptValidation(c)
	}
	// Depth-3 leaves (harness record) require kit/hierarchical on
	// every intermediate; the harness group annotates itself, the
	// conformance parent is annotated here.
	cli.SetHierarchical(cmd)
	cmd.AddCommand(verify, install, stories, gradeCmd, badgeCmd, static, harness, generateStories, svc)
	return cmd
}

// Conformance exit-code codes. These are conformance-tree-local and
// extend output.Code*. USAGE and IO reuse the kit-wide numeric slots
// they belong to (2 usage, 6 transient); the tool-specific outcomes
// get their own band slots so the exit code alone tells an agent
// which class it is. The Code string then refines within the class
// for JSON consumers.
const (
	CodeLeakDetected = "LEAK_DETECTED" // exit 66 (ExitLeakDetected)
	CodeUsage        = "USAGE"         // exit 2 (kit-wide usage slot)
	CodeIO           = "IO"            // exit 6 (kit-wide transient slot)
	CodeConfig       = "CONFIG"        // exit 67 (ExitConfigError)
)

// Conformance-tree band codes. The spec reserves 0-6 for the shared
// taxonomy and leaves >6 to documented per-tool codes; kit already
// allocates 64 (RATE_LIMITED) and 65 (PROVENANCE_MISSING) in
// go/console/output. The conformance tree extends that band
// contiguously so no two kit features can claim the same slot:
//
//	64 RATE_LIMITED        (output.ExitRateLimited)
//	65 PROVENANCE_MISSING  (output.ExitProvenanceMissing)
//	66 LEAK_DETECTED       (this package)
//	67 CONFIG              (this package)
//	68 GRADE_FAIL          (go/conformance/client)
//	69 GRADE_UNGRADABLE    (go/conformance/client)
const (
	// ExitLeakDetected is the exit code for a verify-no-leak /
	// verify-stories finding. A leak is a tool-specific verdict, not
	// one of the shared failure classes, so it lives in the band.
	ExitLeakDetected = 66

	// ExitConfigError is the exit code for malformed conformance
	// configuration (.verifynoleak.allow, rules files, bare ignore
	// comments). Also tool-specific: the shared taxonomy has no
	// config-error class and overloading 5 (auth) or 2 (usage) would
	// make agents mis-branch.
	ExitConfigError = 67
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
	// finding. Compare with errors.Is. Exit code 66 (band).
	ErrLeakDetected = &conformanceSentinel{code: CodeLeakDetected, exit: ExitLeakDetected, transience: output.TransiencePermanent, msg: "verify-no-leak: scenario-shaped content detected"}

	// ErrUsage is the identity sentinel for bad flags or invocations
	// of reserved-but-unimplemented subcommands. Exit code 2.
	ErrUsage = &conformanceSentinel{code: CodeUsage, exit: 2, transience: output.TransiencePermanent, msg: "conformance: usage error"}

	// ErrIO is the identity sentinel for git/gh/environment failures
	// the caller should retry. Exit code 6 (transient class).
	ErrIO = &conformanceSentinel{code: CodeIO, exit: output.ExitTransient, transience: output.TransienceTransient, msg: "conformance: io error"}

	// ErrConfig is the identity sentinel for malformed
	// .verifynoleak.allow or a bare ignore comment missing its reason.
	// Exit code 67 (band).
	ErrConfig = &conformanceSentinel{code: CodeConfig, exit: ExitConfigError, transience: output.TransiencePermanent, msg: "conformance: config error"}
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
	code       string
	exit       int
	transience string
	msg        string
}

func (s *conformanceSentinel) Error() string { return s.msg }

func (s *conformanceSentinel) AsCLIError() *output.Error {
	return &output.Error{Code: s.code, Message: s.msg, ExitCode: s.exit, Transience: s.transience}
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
		Transience:   w.base.transience,
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
		return ExitLeakDetected, true
	case errors.Is(err, ErrUsage):
		return 2, true
	case errors.Is(err, ErrIO):
		return output.ExitTransient, true
	case errors.Is(err, ErrConfig):
		return ExitConfigError, true
	}
	return 0, false
}

// reservedCmd returns a placeholder subcommand for names owned by a
// sibling track that has not yet implemented its conformance layer.
// Invoking the placeholder exits 2 (usage) with a pointer to the
// owning track.
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
