package stage

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/core/stage"
)

// showRow is the table/json/yaml shape for `stage show`.
type showRow struct {
	Scope  string `table:"SCOPE"  json:"scope"  yaml:"scope"`
	Stage  string `table:"STAGE"  json:"stage"  yaml:"stage"`
	Since  string `table:"SINCE"  json:"since"  yaml:"since"`
	Until  string `table:"UNTIL"  json:"until,omitempty"  yaml:"until,omitempty"`
	Reason string `table:"REASON" json:"reason,omitempty" yaml:"reason,omitempty"`
	Actor  string `table:"ACTOR"  json:"actor,omitempty"  yaml:"actor,omitempty"`
}

func showCmd(cfg Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show [scope]",
		Short: "Print the current stage State for a scope",
		Long: `Print the current stage State for the named scope. With no argument, falls back
to the default scope from the configured ProjectResolver.

When the scope has no stage set, prints "stage: active (default)".`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope := resolveScope(args, cfg)
			if scope == "" {
				return fmt.Errorf("stage: scope required (no project resolver configured)")
			}
			st, err := stage.Read(scope)
			if err != nil {
				return err
			}
			format := viper.GetString("format")
			if format == "" {
				format = output.Table
			}
			row := stateToRow(scope, st)
			if format == output.Table && st.Stage == stage.StageActive && st.Since.IsZero() {
				cmd.Printf("stage: active (default)\n")
				return nil
			}
			return output.Render(cmd.OutOrStdout(), format, []showRow{row})
		},
	}
	return cmd
}

// stateToRow flattens a stage.State into the table-friendly showRow.
func stateToRow(scope string, st stage.State) showRow {
	row := showRow{
		Scope:  scope,
		Stage:  string(st.Stage),
		Reason: st.Reason,
		Actor:  st.Actor,
	}
	if !st.Since.IsZero() {
		row.Since = st.Since.UTC().Format(time.RFC3339)
	}
	if st.Until != nil {
		row.Until = st.Until.UTC().Format(time.RFC3339)
	}
	return row
}
