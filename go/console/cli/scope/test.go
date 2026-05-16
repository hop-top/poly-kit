package scope

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	kitcli "hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	scopepkg "hop.top/kit/go/core/scope"
)

func testCmd() *cobra.Command {
	var (
		tool string
		op   string
	)
	cmd := &cobra.Command{
		Use:   "test <path>...",
		Short: "Bulk-check multiple paths; exit 1 if any are denied",
		Long: "Run the kit/scope decision against each supplied path. " +
			"Exit 1 when ANY decision denies (or the policy is strict + " +
			"any decision is unknown). Output mirrors `scope check` per " +
			"row so CI pipelines can diff individual verdicts.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pol, err := resolvePolicy(tool)
			if err != nil {
				return err
			}
			parsedOp, err := parseOp(op)
			if err != nil {
				return usageError(err)
			}

			rows := make([]checkResult, 0, len(args))
			anyDenied := false
			for _, path := range args {
				dec, cerr := pol.Check(scopepkg.Path(path), parsedOp)
				if cerr != nil {
					return cerr
				}
				rows = append(rows, checkResult{
					Path:     path,
					Op:       opLabel(parsedOp),
					Decision: decisionName(dec),
					Tool:     tool,
				})
				if dec == scopepkg.Denied {
					anyDenied = true
				}
				if dec == scopepkg.Unknown && pol.Mode() == scopepkg.Strict {
					anyDenied = true
				}
			}

			if err := output.Dispatch(cmd, viper.GetViper(), rows); err != nil {
				return err
			}

			if anyDenied {
				return makeExitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "", "Load policy via FromConfig(<tool>) instead of Default()")
	cmd.Flags().StringVar(&op, "op", "read", "Operation: read | write | exec")
	kitcli.SetSideEffect(cmd, kitcli.SideEffectRead)
	kitcli.SetIdempotency(cmd, kitcli.IdempotencyYes)
	return cmd
}
