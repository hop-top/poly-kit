// Package telemetry implements the `kit telemetry` cobra subtree:
// the user-facing CLI for inspecting, mutating, auditing, and resetting
// the kit telemetry consent decision (kit-consent track, ADR-0036).
//
// Files in this package (one subcommand per file):
//
//	cmd.go     — this file; owns the `kit telemetry` parent command and
//	             its AddCommand list. Subcommand files each expose a
//	             *Cmd() (e.g. statusCmd, promptCmd, enableCmd) that
//	             this file wires in.
//	prompt.go  — first-run interactive prompt (T-0665).
//	status.go  — `kit telemetry status` (T-0666, this delivery).
//	enable.go  — `kit telemetry enable`  (T-0667, future).
//	disable.go — `kit telemetry disable` (T-0667, future).
//	reset.go   — `kit telemetry reset`   (T-0668, future).
//	inspect.go — `kit telemetry inspect` (T-0669, future).
//
// MERGE PROTOCOL: cmd.go is the single merge point for the telemetry
// subtree. Every sibling task (T-0665 / T-0667 / T-0668 / T-0669) adds
// exactly one AddCommand line below as it lands. Subcommand bodies and
// their tests live in dedicated per-task files; sibling tasks MUST NOT
// touch cmd.go's metadata (Use / Short / Long) — only append a single
// AddCommand line in the marked block.
package telemetry

import "github.com/spf13/cobra"

// Cmd returns the `kit telemetry` cobra root. The parent command is a
// pure container; every meaningful behavior lives in a leaf
// subcommand. The Long description names the subcommands a user can
// run next so `kit telemetry --help` is self-orienting without
// requiring `kit telemetry status` first.
func Cmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "telemetry",
		Short: "Manage anonymous usage telemetry",
		Long: `Manage kit's anonymous usage telemetry.

kit can ship command-level telemetry (off by default) to help improve
the tooling. Use these subcommands to view, change, audit, or reset
the consent decision.

Default is denied. See ` + "`kit telemetry status`" + ` for the current state.`,
	}

	// --- AddCommand list (merge point for T-0665..T-0669) ---
	// Each sibling task appends exactly one line here. Keep the list
	// alphabetical so concurrent edits collide on intent rather than
	// position. Do not reflow this block — diff hygiene matters more
	// than tight grouping.
	c.AddCommand(disableCmd())
	c.AddCommand(enableCmd())
	c.AddCommand(inspectCmd())
	c.AddCommand(resetCmd())
	c.AddCommand(statusCmd())
	// --- end AddCommand list ---

	return c
}
