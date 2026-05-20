// reset.go implements `kit telemetry reset`: clears the persisted
// consent decision (back to StateUnknown) AND rotates the anonymous
// installation_id so the next event stream is unlinkable from the
// prior one. This is the operator's "fresh start" lever for telemetry
// trust posture.
//
// Reset is marked SideEffectDestructive so the kit-wide --confirm
// matrix (yes/no/auto/prompt — see hop.top/kit/go/console/cli/policy_runE.go)
// gates it automatically when the parent `kit` root wraps the
// telemetry tree through cli.New. We also expose a local --yes flag
// (matches the `kit breaker reset --yes` precedent) so direct
// invocations — tests, scripts that don't route through the wrap,
// operators on a non-TTY without --confirm wiring — have a stable
// affirmative answer. The two paths compose: --yes makes runReset
// skip its inline prompt; the policy wrap's own --confirm gate runs
// independently before RunE is even reached.
//
// Atomicity: we Clear consent first, THEN Rotate. If Clear succeeds
// but Rotate fails, the on-disk state is "consent=unknown, install_id
// unchanged" — a partial-failure shape that the operator unwinds by
// re-running reset (Clear is idempotent; Rotate retries cleanly).
// No transactional rollback is attempted: an explicit, idempotent
// retry is simpler reasoning than a half-baked compensating Set.

package telemetry

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/core/consent"
	runtimetel "hop.top/kit/go/runtime/telemetry"
)

// resetCmd builds the `kit telemetry reset` leaf. RunE delegates to
// runReset so the body stays exercisable from tests without spinning
// a cobra invocation. Marked SideEffectDestructive so the kit-wide
// --confirm matrix engages when wrapped through cli.New.
func resetCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Clear telemetry consent and rotate the anonymous install_id",
		Long: `Clear the persisted telemetry consent and rotate the anonymous
installation_id. The next interactive run will re-prompt.

This drops the link between the prior event stream and any future
events — a "fresh start" for the operator's telemetry trust posture.

This is a destructive action. Pass --yes to skip the inline
confirmation prompt. The kit-wide --confirm flag (yes|no|auto|prompt)
also applies; both gates compose.`,
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			// Re-running rotates a NEW install_id each call, so not
			// idempotent (each invocation mints a fresh anonymous
			// identity, unlinkable from prior).
			"kit/idempotent": "no",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReset(
				cmd.Context(),
				cmd.InOrStdin(),
				cmd.OutOrStdout(),
				yes,
			)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false,
		"Skip the inline confirmation prompt (destructive action)")
	cli.SetSideEffect(cmd, cli.SideEffectDestructive)
	return cmd
}

// runReset is the testable core of `kit telemetry reset`. Order:
//
//  1. If autoConfirm is false, read a y/N answer from stdin. Anything
//     other than an explicit "y"/"yes" (case-insensitive) aborts.
//  2. Open the consent FileStore and call Clear — drops the persisted
//     decision back to StateUnknown. Other top-level YAML keys are
//     preserved by FileStore.Clear.
//  3. Call runtimetel.Rotate to atomically swap in 32 fresh
//     crypto/rand bytes for the install_id file. Returns the new
//     hex-sha256 identifier.
//  4. Print a confirmation naming both effects + the new identifier.
//
// Partial-failure shape: a successful Clear followed by a failed
// Rotate leaves "consent=unknown, install_id unchanged". The operator
// re-runs reset; Clear is a no-op on an already-cleared store and
// Rotate retries from scratch. We surface the Rotate error verbatim
// so the operator can diagnose the underlying I/O issue.
func runReset(ctx context.Context, stdin io.Reader, stdout io.Writer, autoConfirm bool) error {
	if !autoConfirm {
		if !confirmReset(stdin, stdout) {
			return fmt.Errorf("reset aborted")
		}
	}

	store, err := consent.NewFileStore()
	if err != nil {
		return fmt.Errorf("reset: open consent store: %w", err)
	}
	if err := store.Clear(ctx); err != nil {
		return fmt.Errorf("reset: clear consent: %w", err)
	}

	newID, err := runtimetel.Rotate()
	if err != nil {
		// Partial-failure state: consent is cleared but install_id is
		// untouched. The error message names the partial state so the
		// operator knows what to expect on the next `kit telemetry
		// status` read and that a re-run is safe.
		return fmt.Errorf("reset: rotate install_id (consent already cleared; safe to re-run): %w", err)
	}

	fmt.Fprintf(stdout,
		"Telemetry reset complete.\n  Consent: cleared (state=unknown)\n  Install ID: rotated to %s\n  Next interactive run will re-prompt.\n",
		newID,
	)
	return nil
}

// confirmReset renders the y/N prompt and reads one line from stdin.
// Mirrors the askYesNo helper in prompt.go: bufio.Scanner over
// fmt.Fscanln so EOF on a closed stdin returns false (aborted)
// without a distinct error path. Default highlighted answer is No,
// matching the kit-wide promptConfirm convention in
// hop.top/kit/go/console/cli/policy_runE.go.
func confirmReset(stdin io.Reader, stdout io.Writer) bool {
	_, _ = fmt.Fprintln(stdout, "This will clear consent and rotate install_id. Continue?")
	_, _ = fmt.Fprint(stdout, "[y/N]: ")

	sc := bufio.NewScanner(stdin)
	if !sc.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(sc.Text()))
	return answer == "y" || answer == "yes"
}
