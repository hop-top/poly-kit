package breaker

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	kitcli "hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"

	bpkg "hop.top/kit/go/core/breaker"
)

// listRow is a flattened view for table/json/yaml of one breaker.
type listRow struct {
	Name       string `table:"NAME"   json:"name"   yaml:"name"`
	State      string `table:"STATE"  json:"state"  yaml:"state"`
	Trips      uint64 `table:"TRIPS"  json:"trips"  yaml:"trips"`
	LastReason string `table:"REASON" json:"last_reason" yaml:"last_reason"`
}

func listCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every registered breaker",
		Long: "List the runtime circuit breakers registered in this " +
			"process with their current state (closed|open|half-open), " +
			"trip count, and last trip reason. Cross-process introspection " +
			"is out of scope — IPC required.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rows := []listRow{}
			for _, b := range bpkg.List() {
				s := b.Stats()
				rows = append(rows, listRow{
					Name:       b.Name(),
					State:      b.State().String(),
					Trips:      s.Trips,
					LastReason: s.LastTripReason,
				})
			}
			return output.Dispatch(cmd, viper.GetViper(), rows)
		},
	}
	kitcli.SetSideEffect(cmd, kitcli.SideEffectRead)
	kitcli.SetIdempotency(cmd, kitcli.IdempotencyYes)
	return cmd
}
