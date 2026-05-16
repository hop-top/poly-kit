package cli

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/console/output"
)

// Policy globals (§8.6). Registered automatically on the root in
// cli.New unless explicitly disabled.
const (
	confirmFlag      = "confirm"
	maxOpsFlag       = "max-ops"
	policyFlag       = "policy"
	confirmTokenFlag = "confirm-token"
)

// destructiveTokenAnnotation marks a destructive command that
// requires --confirm-token=<sha> in addition to --confirm. The
// annotation value should be "required" for opt-in; absent means
// regular destructive behavior.
//
// Reserved under the kit/ prefix per §3.5.
const destructiveTokenAnnotation = "kit/destructive-token"

// confirmMode is the parsed --confirm value.
type confirmMode string

const (
	confirmAuto   confirmMode = "auto"
	confirmYes    confirmMode = "yes"
	confirmNo     confirmMode = "no"
	confirmPrompt confirmMode = "prompt"
)

// PolicyLoader resolves a named policy to a Policy. Adopters supply
// one via WithPolicy; the default loader (DefaultPolicyLoader) reads
// from $XDG_CONFIG_HOME/<tool>/policies/<name>.yaml.
type PolicyLoader func(name string) (policy.Policy, error)

// DefaultPolicyLoader builds a PolicyLoader that resolves names
// against $XDG_CONFIG_HOME/<tool>/policies/<name>.yaml. Tool is the
// binary name (cli.Config.Name).
func DefaultPolicyLoader(tool string) PolicyLoader {
	return func(name string) (policy.Policy, error) {
		return policy.LoadNamed(tool, name)
	}
}

// WithPolicy installs the policy loader. When --policy=<name> is
// passed at invocation time the middleware calls loader(name) to
// resolve the policy file. Pass nil to disable policy-file support
// (the --confirm and --max-ops flags still work).
func WithPolicy(loader PolicyLoader) func(*Root) {
	return func(r *Root) {
		r.policyLoader = loader
	}
}

// promptInputFn is the prompt input source — overridable in tests.
// Default reads cmd.InOrStdin().
var promptInputFn = func(cmd *cobra.Command) io.Reader {
	return cmd.InOrStdin()
}

// promptIsTTYFn reports whether the prompt source is a terminal.
// Tests override to force prompt-mode resolution.
var promptIsTTYFn = func(cmd *cobra.Command) bool {
	if f, ok := cmd.InOrStdin().(*os.File); ok {
		return isatty.IsTerminal(f.Fd())
	}
	return false
}

// resolveConfirmMode parses the raw --confirm flag value, applying the
// matrix in §8.6: empty value defaults to "prompt" on a TTY and "no"
// otherwise. Invalid values default to "prompt"; the cli flag
// registration uses string typing so the validator catches typos.
func resolveConfirmMode(cmd *cobra.Command, raw string) confirmMode {
	switch confirmMode(strings.ToLower(strings.TrimSpace(raw))) {
	case confirmYes:
		return confirmYes
	case confirmNo:
		return confirmNo
	case confirmAuto:
		return confirmAuto
	case confirmPrompt:
		return confirmPrompt
	}
	if promptIsTTYFn(cmd) {
		return confirmPrompt
	}
	return confirmNo
}

// flagValue returns the persistent flag's string value visible to cmd,
// walking up the parent chain. Empty when the flag isn't registered.
func flagValue(cmd *cobra.Command, name string) string {
	for c := cmd; c != nil; c = c.Parent() {
		if f := c.PersistentFlags().Lookup(name); f != nil {
			return f.Value.String()
		}
		if f := c.Flags().Lookup(name); f != nil {
			return f.Value.String()
		}
	}
	return ""
}

// flagInt returns the int value of a persistent flag, or 0 when
// missing/unparseable.
func flagInt(cmd *cobra.Command, name string) int {
	s := flagValue(cmd, name)
	if s == "" {
		return 0
	}
	var n int
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n
}

// destructiveTokenSha returns the canonical SHA the user MUST echo
// back via --confirm-token. Stable across invocations of the same
// command path so the prompt can be deterministic for tests/scripts
// that pre-compute it. The full sha256 is truncated to 12 hex chars
// for human-friendly entry; full-width comparison still works because
// users paste exactly what was printed.
func destructiveTokenSha(cmd *cobra.Command) string {
	h := sha256.Sum256([]byte(cmd.CommandPath()))
	return hex.EncodeToString(h[:6])
}

// newPolicyEngine builds a policy.Engine from the active flags. When
// --policy is unset it returns an Engine with an empty Policy
// (default-permit) parameterized by --max-ops only.
func (r *Root) newPolicyEngine(cmd *cobra.Command) (*policy.Engine, error) {
	maxOps := flagInt(cmd, maxOpsFlag)
	policyName := flagValue(cmd, policyFlag)

	var p policy.Policy
	if policyName != "" {
		if r.policyLoader == nil {
			return nil, output.UsageError(
				"--policy is set but no policy loader is wired (cli.WithPolicy)",
			)
		}
		loaded, err := r.policyLoader(policyName)
		if err != nil {
			return nil, output.UnauthorizedError(
				fmt.Sprintf("policy %q: %v", policyName, err),
			)
		}
		p = loaded
	}
	return policy.NewEngine(p, maxOps), nil
}

// renderPolicyError writes ce to cmd's stderr in the active --format
// and silences cobra so the envelope isn't double-printed. Mirrors
// the post-RunE error_render path so policy failures look identical
// to adopter-returned typed errors.
func renderPolicyError(cmd *cobra.Command, ce *output.Error) error {
	format := activeFormat(cmd)
	_ = output.RenderError(cmd.ErrOrStderr(), format, ce)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	return ce
}

// promptConfirm renders the y/N prompt to stderr and reads one line
// from cmd.InOrStdin. Returns true when the answer is "y"/"yes"
// (case-insensitive). EOF / blank → false ("aborted").
func promptConfirm(cmd *cobra.Command, question string) bool {
	fmt.Fprint(cmd.ErrOrStderr(), question+" [y/N] ")
	r := bufio.NewReader(promptInputFn(cmd))
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// requiresDestructiveToken reports whether cmd has opted into the
// typed-confirmation flow via the kit/destructive-token annotation.
func requiresDestructiveToken(cmd *cobra.Command) bool {
	if cmd.Annotations == nil {
		return false
	}
	v := cmd.Annotations[destructiveTokenAnnotation]
	return v == "required" || v == "true"
}

// installPolicyFlags registers --confirm-token on every destructive
// leaf that opts in via the kit/destructive-token annotation. Other
// destructive leaves don't need the flag.
//
// Idempotent: re-registration of the same flag is a no-op.
func installConfirmTokenFlag(cmd *cobra.Command) {
	walk(cmd, func(c *cobra.Command) {
		if !isLeaf(c) || isBuiltin(c) || !c.Runnable() {
			return
		}
		if !requiresDestructiveToken(c) {
			return
		}
		if c.Flags().Lookup(confirmTokenFlag) != nil {
			return
		}
		c.Flags().String(confirmTokenFlag, "",
			"Typed confirmation token (sha) printed by the command's prompt. Required for kit/destructive-token commands.")
	})
}

// wrapPolicyRunE wraps inner so the policy gates fire BEFORE inner
// (which is the idempotency+error-render+adopter chain) runs.
//
// Pre-flight (in order):
//
//  1. Resolve the active confirm mode.
//  2. Read the side-effect tag.
//  3. If destructive: enforce the --confirm matrix; on prompt-mode,
//     prompt; on rejection, abort UNAUTHORIZED. Skipped under
//     --dry-run since dry runs make no real side-effect.
//  4. If a policy is active: ask Engine.Authorize. Refuse when not
//     allowed. Lift policy-mandated require_confirm into the prompt
//     gate too.
//  5. If the command requires a typed confirmation token (annotation
//     kit/destructive-token=required), validate --confirm-token.
//
// Post-flight (only when inner returned nil and the side-effect is
// write|destructive): RecordOp on the engine. ErrMaxOpsExceeded ->
// rendered as RATE_LIMITED with exit code 64.
func (r *Root) wrapPolicyRunE(
	_ *cobra.Command, // captured for parity with wrapRunE; closure uses its own cmd
	inner func(*cobra.Command, []string) error,
) func(*cobra.Command, []string) error {
	if inner == nil {
		return nil
	}
	return func(cmd *cobra.Command, args []string) error {
		se, hasSE := GetSideEffect(cmd)

		// Build the policy engine for this invocation. Cheap; it just
		// reads the flag values and (optionally) loads YAML.
		engine, err := r.newPolicyEngine(cmd)
		if err != nil {
			if ce, ok := err.(*output.Error); ok {
				return renderPolicyError(cmd, ce)
			}
			return renderPolicyError(cmd, &output.Error{
				Code: output.CodeGeneric, Message: err.Error(), ExitCode: 1,
			})
		}

		// Policy gate first — refusals here aren't bypassed by
		// --confirm=yes (per the "Do NOT" note in §8.6).
		var policyConfirm bool
		if hasSE {
			allowed, requireConfirm, reason := engine.Authorize(cmd)
			if !allowed {
				return renderPolicyError(cmd,
					output.UnauthorizedError(reason))
			}
			policyConfirm = requireConfirm
		}

		// Confirmation gate — only meaningful for destructive ops, or
		// when policy.require_confirm matched the verb. --dry-run
		// short-circuits: dry runs have no real side-effect to confirm.
		if !IsDryRun(cmd) {
			if hasSE && (isDestructiveLike(se) || policyConfirm) {
				if err := r.gateConfirm(cmd, se); err != nil {
					return err
				}
			}
		}

		// Run the inner chain (idempotency → error-render → adopter).
		if err := inner(cmd, args); err != nil {
			return err
		}

		// Post-flight ops budget: only count mutating ops.
		if hasSE && (isWriteLike(se) || isDestructiveLike(se)) {
			// Don't account dry-run mutations against the budget.
			if !IsDryRun(cmd) {
				if rerr := engine.RecordOp(cmd); rerr != nil {
					return renderPolicyError(cmd, output.RateLimitedError(
						"max-ops budget exceeded after running "+cmd.CommandPath(),
					))
				}
			}
		}
		return nil
	}
}

// gateConfirm enforces the --confirm + --confirm-token matrix for the
// given side-effect class. Returns a rendered *output.Error when the
// user is refused or fails the prompt; nil to proceed.
func (r *Root) gateConfirm(cmd *cobra.Command, se SideEffect) error {
	mode := resolveConfirmMode(cmd, flagValue(cmd, confirmFlag))
	tokenRequired := requiresDestructiveToken(cmd)

	// Typed-token destructives never let --confirm=yes alone proceed:
	// the caller must also pass --confirm-token=<sha> matching the
	// command's deterministic sha.
	if tokenRequired {
		expected := destructiveTokenSha(cmd)
		got := flagValue(cmd, confirmTokenFlag)
		if got == "" {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"This is a destructive operation requiring typed confirmation.\nToken: %s\nRe-run with --confirm-token=%s to proceed.\n",
				expected, expected)
			return renderPolicyError(cmd, output.UnauthorizedError(
				"destructive command "+cmd.CommandPath()+" requires --confirm-token="+expected,
			))
		}
		if got != expected {
			return renderPolicyError(cmd, output.UnauthorizedError(
				"--confirm-token mismatch for "+cmd.CommandPath()+
					"; expected "+expected,
			))
		}
		// Token matches. Even with --confirm=no/auto/prompt, we proceed.
		return nil
	}

	switch mode {
	case confirmYes, confirmAuto:
		return nil
	case confirmNo:
		return renderPolicyError(cmd, output.UnauthorizedError(
			"destructive command "+cmd.CommandPath()+
				" refused: --confirm=no (or non-TTY default)",
		))
	case confirmPrompt:
		q := fmt.Sprintf("This is a %s operation (%s). Continue?",
			se, cmd.CommandPath())
		if !promptConfirm(cmd, q) {
			return renderPolicyError(cmd, output.UnauthorizedError(
				"aborted by user at confirm prompt for "+cmd.CommandPath(),
			))
		}
		return nil
	}
	// Unreachable in practice — resolveConfirmMode normalizes values.
	return nil
}
