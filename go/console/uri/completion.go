package uri

import (
	"fmt"

	"github.com/spf13/cobra"
)

func shellCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion <bash|zsh|fish|powershell>",
		Short: "Generate shell completion for this CLI",
		Long:  "Generate shell completion scripts for the root CLI command.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return root.GenPowerShellCompletion(cmd.OutOrStdout())
			default:
				return fmt.Errorf("unknown shell %q (want bash|zsh|fish|powershell)", args[0])
			}
		},
	}
	annotateRead(cmd)
	return cmd
}
