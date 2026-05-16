package stage

import (
	"sort"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/core/projects"
	"hop.top/kit/go/core/stage"
)

func listCmd(_ Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Print every scope in projects.yaml with its current stage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			file, err := projects.Read()
			if err != nil {
				return err
			}
			rows := make([]showRow, 0, len(file.Projects))
			for name, entry := range file.Projects {
				st := stage.State{Stage: stage.StageActive}
				if entry.Stage != nil && entry.Stage.Stage != "" {
					st.Stage = stage.Stage(entry.Stage.Stage)
					st.Since = entry.Stage.Since
					st.Until = entry.Stage.Until
					st.Reason = entry.Stage.Reason
					st.Actor = entry.Stage.Actor
				}
				rows = append(rows, stateToRow(name, st))
			}
			sort.SliceStable(rows, func(i, j int) bool {
				return rows[i].Scope < rows[j].Scope
			})
			format := viper.GetString("format")
			if format == "" {
				format = output.Table
			}
			return output.Render(cmd.OutOrStdout(), format, rows)
		},
	}
	return cmd
}

// suppress the unused-import warning when output.Render is the only
// consumer — keep the time import referenced.
var _ = time.RFC3339
