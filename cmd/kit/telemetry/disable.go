// disable.go implements `kit telemetry disable`: persists a denied
// consent decision (StateDenied, SourceFlag) so subsequent telemetry
// events stop shipping.
//
// Disable does NOT check env kill switches the way enable does — a
// denied state IS the env-blocked outcome, so writing it never
// disagrees with the resolver. The operator can always disable, even
// while DO_NOT_TRACK=1 is already in their shell; the on-disk denial
// persists across env changes for free.
//
// Honors the kit-wide --dry-run via cli.IsDryRun: when true, prints
// what WOULD be written and returns without touching the store.

package telemetry

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/core/consent"
)

// disableCmd builds the `kit telemetry disable` leaf. Same shape as
// enableCmd; the env-block check is intentionally absent because a
// denied write is always safe regardless of env.
func disableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable",
		Short: "Deny consent for kit anonymous usage telemetry",
		Long: `Deny consent for anonymous telemetry. Persists the decision with
decision_source=flag.

Disable is always allowed — env kill switches such as DO_NOT_TRACK=1
already produce the same denied outcome, so writing a persistent
denial on top of them is harmless and survives env changes.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDisable(cmd.Context(), cmd.OutOrStdout(), cli.IsDryRun(cmd))
		},
	}
}

// runDisable is the testable core. dryRun=true skips the store write.
func runDisable(ctx context.Context, stdout io.Writer, dryRun bool) error {
	d := consent.Decision{
		State:          consent.StateDenied,
		DecidedAt:      time.Now().UTC(),
		PromptVersion:  PromptVersion,
		DecisionSource: consent.SourceFlag,
	}

	if dryRun {
		_, _ = fmt.Fprintln(stdout, "dry-run: would persist consent state=denied source=flag")
		return nil
	}

	store, err := consent.NewFileStore()
	if err != nil {
		return fmt.Errorf("disable: open consent store: %w", err)
	}
	if err := store.Set(ctx, d); err != nil {
		return fmt.Errorf("disable: persist decision: %w", err)
	}

	_, _ = fmt.Fprintln(stdout, "Telemetry disabled. Run `kit telemetry status` to verify.")
	return nil
}
