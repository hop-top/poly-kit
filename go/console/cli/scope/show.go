package scope

import (
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	kitcli "hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	scopepkg "hop.top/kit/go/core/scope"
)

// showRow is a flattened view of one Rule for table/json/yaml output.
type showRow struct {
	Verdict string `table:"VERDICT" json:"verdict" yaml:"verdict"`
	Op      string `table:"OPS"     json:"op"      yaml:"op"`
	Pattern string `table:"PATTERN" json:"pattern" yaml:"pattern"`
}

// showOutput is the top-level shape for json/yaml output of `kit scope show`.
type showOutput struct {
	Mode  string    `json:"mode"  yaml:"mode"`
	Tool  string    `json:"tool,omitempty"  yaml:"tool,omitempty"`
	Rules []showRow `json:"rules" yaml:"rules"`
}

func showCmd() *cobra.Command {
	var tool string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the effective scope policy",
		Long: "Print every Allow/Deny rule in the active kit/scope " +
			"policy, sorted by verdict then pattern. --tool <name> " +
			"resolves the policy via FromConfig instead of Default.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pol, err := resolvePolicy(tool)
			if err != nil {
				return err
			}
			out := showOutput{
				Mode:  modeName(pol.Mode()),
				Tool:  tool,
				Rules: rulesToRows(pol.Rules()),
			}

			format := viper.GetString("format")
			if format == "" {
				format = output.Table
			}
			switch format {
			case output.Table:
				// Header line for table mode: "MODE: strict (tool=foo)"
				header := "MODE: " + out.Mode
				if tool != "" {
					header += " (tool=" + tool + ")"
				}
				cmd.Println(header)
				return output.Render(cmd.OutOrStdout(), output.Table, out.Rules)
			default:
				return output.Render(cmd.OutOrStdout(), format, out)
			}
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "", "Load policy via FromConfig(<tool>) instead of Default()")
	kitcli.SetSideEffect(cmd, kitcli.SideEffectRead)
	kitcli.SetIdempotency(cmd, kitcli.IdempotencyYes)
	return cmd
}

// rulesToRows flattens [Rule] into [showRow], sorted Deny-first then Allow-first
// then alphabetically by pattern.
func rulesToRows(rules []scopepkg.Rule) []showRow {
	rows := make([]showRow, 0, len(rules))
	for _, r := range rules {
		verdict := "DENY"
		if r.Allow {
			verdict = "ALLOW"
		}
		op := opLabel(r.Ops)
		for _, p := range r.Patterns {
			rows = append(rows, showRow{Verdict: verdict, Op: op, Pattern: string(p)})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Verdict != rows[j].Verdict {
			return rows[i].Verdict < rows[j].Verdict // ALLOW < DENY alphabetically
		}
		if rows[i].Op != rows[j].Op {
			return rows[i].Op < rows[j].Op
		}
		return rows[i].Pattern < rows[j].Pattern
	})
	return rows
}
