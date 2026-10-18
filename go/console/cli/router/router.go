// Package router provides the "kit llm router" CLI subcommand tree for
// managing RouteLLM server instances.
//
// Subcommands: start, stop, list, config.
package router

import (
	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/cmdmeta"
)

// Cmd returns the top-level "router" command with all subcommands attached.
//
// The tree is kit-reserved wherever it is mounted (cmdmeta.KeyReserved):
// it starts and kills server processes on the host, so no served
// surface lists or runs it. start, which starts a server, is
// self-hosting besides.
func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "router",
		Short: "Manage the routellm server",
		Args:  cobra.NoArgs,
	}
	cmdmeta.SetReserved(cmd)

	// Every leaf declares its own side effect; start, which starts a
	// server, is self-hosting too.
	start := startCmd()
	if start.Annotations == nil {
		start.Annotations = make(map[string]string)
	}
	start.Annotations[cmdmeta.KeySelfHosting] = "true"
	cmd.AddCommand(
		start,
		stopCmd(),
		listCmd(),
		configCmd(),
	)

	return cmd
}
