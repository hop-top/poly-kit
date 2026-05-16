// Package router provides the "kit llm router" CLI subcommand tree for
// managing RouteLLM server instances.
//
// Subcommands: start, stop, list, config.
package router

import (
	"github.com/spf13/cobra"
)

// Cmd returns the top-level "router" command with all subcommands attached.
func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "router",
		Short: "Manage the routellm server",
		Args:  cobra.NoArgs,
	}

	cmd.AddCommand(
		startCmd(),
		stopCmd(),
		listCmd(),
		configCmd(),
	)

	return cmd
}
