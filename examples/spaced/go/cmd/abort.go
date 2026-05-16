package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"hop.top/kit/examples/spaced/go/data"
	"hop.top/kit/go/console/cli"
)

// AbortCmd returns the `abort <mission>` command.
func AbortCmd(root *cli.Root) *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:   "abort <mission>",
		Short: "Abort a mission",
		Long:  "Abort a named mission. Specify --reason for the record.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, ok := data.FindMission(args[0])
			if !ok {
				fmt.Fprintf(os.Stderr, "mission not found: %s\n", args[0])
				return fmt.Errorf("mission not found: %s", args[0])
			}

			if reason == "" {
				reason = "Unspecified. SpaceX calls this a 'hold for anomaly investigation'."
			}

			fmt.Println()
			fmt.Printf("  ✗ ABORT INITIATED: %s\n", m.Name)
			fmt.Printf("  Vehicle : %s\n", m.Vehicle)
			fmt.Printf("  Reason  : %s\n", reason)
			fmt.Println()
			fmt.Println("  Mission aborted. The FAA has been notified.")
			fmt.Println("  Elon will tweet shortly. Probably something positive.")
			fmt.Println()

			return nil
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "Reason for abort (optional but appreciated)")
	return cmd
}
