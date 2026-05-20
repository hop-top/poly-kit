// Package telemetry implements the `kit telemetry` cobra subtree:
// the user-facing CLI for inspecting, mutating, auditing, and resetting
// the kit telemetry consent decision (kit-consent track).
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

	// Keep the list alphabetical so concurrent edits collide on intent
	// rather than position.
	c.AddCommand(disableCmd())
	c.AddCommand(enableCmd())
	c.AddCommand(inspectCmd())
	c.AddCommand(resetCmd())
	c.AddCommand(statusCmd())

	return c
}
