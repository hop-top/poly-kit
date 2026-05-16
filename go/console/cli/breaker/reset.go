package breaker

import (
	"errors"

	"github.com/spf13/cobra"
	kitcli "hop.top/kit/go/console/cli"

	bpkg "hop.top/kit/go/core/breaker"
)

func resetCmd() *cobra.Command {
	var (
		all bool
		yes bool
	)
	cmd := &cobra.Command{
		Use:   "reset [<name>]",
		Short: "Reset (close) a breaker, or all with --all",
		Long: "Close the named breaker, returning it to the closed state. " +
			"Pass --all (plus --yes) to reset every registered breaker at " +
			"once. Effects this process only — IPC is required to reach " +
			"breakers in other processes.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case all:
				if !yes {
					return errors.New("breaker reset --all requires --yes")
				}
				bpkg.ResetAll()
				cmd.Println("reset all breakers")
				return nil
			case len(args) == 1:
				b, err := lookupOrError(args[0])
				if err != nil {
					return err
				}
				b.Reset()
				cmd.Printf("reset breaker %q\n", b.Name())
				return nil
			default:
				return errors.New("usage: kit breaker reset <name> | --all [--yes]")
			}
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Reset every registered breaker")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm --all (required)")
	kitcli.SetSideEffect(cmd, kitcli.SideEffectWrite)
	kitcli.SetIdempotency(cmd, kitcli.IdempotencyYes)
	return cmd
}
