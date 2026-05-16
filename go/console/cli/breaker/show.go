package breaker

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	kitcli "hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// counterRow is a flattened view of a single counter for tabular output.
type counterRow struct {
	Name  string `table:"COUNTER" json:"name" yaml:"name"`
	Value int64  `table:"VALUE"   json:"value" yaml:"value"`
}

// showOutput is the structured form for json/yaml output.
type showOutput struct {
	Name           string       `json:"name"             yaml:"name"`
	State          string       `json:"state"            yaml:"state"`
	Trips          uint64       `json:"trips"            yaml:"trips"`
	LastTripAt     time.Time    `json:"last_trip_at"     yaml:"last_trip_at"`
	LastTripReason string       `json:"last_trip_reason" yaml:"last_trip_reason"`
	Counters       []counterRow `json:"counters"         yaml:"counters"`
}

func showCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show full stats for a single breaker",
		Long: "Print the named circuit breaker's state, trip count, " +
			"last-trip metadata, and per-counter values. Exit 1 when " +
			"the name does not resolve in this process's registry.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := lookupOrError(args[0])
			if err != nil {
				return err
			}
			s := b.Stats()
			counters := make([]counterRow, 0, len(s.Counters))
			for k, v := range s.Counters {
				counters = append(counters, counterRow{Name: k, Value: v})
			}
			out := showOutput{
				Name:           b.Name(),
				State:          b.State().String(),
				Trips:          s.Trips,
				LastTripAt:     s.LastTripAt,
				LastTripReason: s.LastTripReason,
				Counters:       counters,
			}

			format := viper.GetString("format")
			if format == "" || format == output.Table {
				cmd.Printf("NAME:    %s\n", out.Name)
				cmd.Printf("STATE:   %s\n", out.State)
				cmd.Printf("TRIPS:   %d\n", out.Trips)
				if !out.LastTripAt.IsZero() {
					cmd.Printf("LAST:    %s (%s)\n", out.LastTripAt.Format(time.RFC3339), out.LastTripReason)
				}
				if len(counters) > 0 {
					return output.Render(cmd.OutOrStdout(), output.Table, counters)
				}
				return nil
			}
			return output.Render(cmd.OutOrStdout(), format, out)
		},
	}
	kitcli.SetSideEffect(cmd, kitcli.SideEffectRead)
	kitcli.SetIdempotency(cmd, kitcli.IdempotencyYes)
	return cmd
}
