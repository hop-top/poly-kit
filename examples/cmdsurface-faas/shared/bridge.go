// Package shared builds a sample [cmdsurface.Bridge] used by both
// the Lambda and Cloud Run FaaS demos. The two cmd/* binaries import
// this package so they share an identical tree and policy — the only
// difference between the two targets is the adapter that fronts the
// bridge.
package shared

import (
	"time"

	"github.com/spf13/cobra"
	"hop.top/kit/go/transport/cmdsurface"
)

// BuildBridge returns a Bridge over a tiny demo command tree.
//
// Tree:
//
//	echo <message>    [safe] — prints the message back
//	ping              [safe] — prints "pong"
//	stamp --who=X     [safe] — prints "stamped by X at <now>"
//
// All three leaves are read-only, so the bridge enables every surface
// by default. AllowDestructiveOn is set anyway so adopters copying
// this example can add destructive leaves without surprises.
func BuildBridge() *cmdsurface.Bridge {
	root := buildTree()
	return cmdsurface.New(root,
		cmdsurface.WithPolicy(cmdsurface.Policy{
			DefaultEnabled: []cmdsurface.Surface{
				cmdsurface.SurfaceCLI,
				cmdsurface.SurfaceFaaS,
				cmdsurface.SurfaceREST,
				cmdsurface.SurfaceSSE,
				cmdsurface.SurfaceMCP,
				cmdsurface.SurfaceWS,
				cmdsurface.SurfaceLib,
			},
			AllowDestructiveOn: []cmdsurface.Surface{
				cmdsurface.SurfaceCLI,
				cmdsurface.SurfaceLib,
			},
		}),
	)
}

// buildTree returns the demo cobra tree. The root is non-runnable;
// each child is a leaf that writes to cmd.OutOrStdout() so the bridge
// captures the output into Result.Stdout.
func buildTree() *cobra.Command {
	root := &cobra.Command{
		Use:   "demo",
		Short: "cmdsurface-faas demo tree",
	}

	echo := &cobra.Command{
		Use:   "echo <message>",
		Short: "Echo the given message back",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for i, a := range args {
				if i > 0 {
					cmd.Print(" ")
				}
				cmd.Print(a)
			}
			cmd.Println()
			return nil
		},
	}

	ping := &cobra.Command{
		Use:   "ping",
		Short: "Reply with pong",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("pong")
			return nil
		},
	}

	stamp := &cobra.Command{
		Use:   "stamp",
		Short: "Print a timestamp tagged with --who",
		RunE: func(cmd *cobra.Command, _ []string) error {
			who, _ := cmd.Flags().GetString("who")
			if who == "" {
				who = "anon"
			}
			cmd.Printf("stamped by %s at %s\n", who, time.Now().Format(time.RFC3339))
			return nil
		},
	}
	stamp.Flags().String("who", "", "name to stamp with")

	root.AddCommand(echo, ping, stamp)
	return root
}
