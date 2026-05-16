package cmd

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/spf13/cobra"
	"hop.top/kit/examples/spaced/go/data"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// telemetryRow is table-renderable telemetry data.
type telemetryRow struct {
	Channel string `table:"CHANNEL"     json:"channel"    yaml:"channel"`
	Value   string `table:"VALUE"       json:"value"      yaml:"value"`
	Unit    string `table:"UNIT"        json:"unit"       yaml:"unit"`
	Status  string `table:"STATUS"      json:"status"     yaml:"status"`
}

// TelemetryCmd returns the `telemetry get <mission>` command tree.
func TelemetryCmd(root *cli.Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetry",
		Short: "Mission telemetry streams",
	}
	cmd.AddCommand(telemetryGetCmd(root))
	return cmd
}

func telemetryGetCmd(root *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "get <mission>",
		Short: "Get telemetry for a mission",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, ok := data.FindMission(args[0])
			if !ok {
				fmt.Fprintf(os.Stderr, "mission not found: %s\n", args[0])
				return fmt.Errorf("mission not found: %s", args[0])
			}

			rng := rand.New(rand.NewSource(time.Now().UnixNano()))
			altitude := rng.Intn(550) + 10
			velocity := rng.Intn(7000) + 1000
			throttle := rng.Intn(40) + 60
			propellant := rng.Intn(80) + 10

			engineStatus := "NOMINAL"
			if m.Outcome == data.OutcomeRUD {
				engineStatus = "RUD*"
			}

			rows := []telemetryRow{
				{Channel: "altitude", Value: fmt.Sprintf("%d", altitude), Unit: "km", Status: "NOMINAL"},
				{Channel: "velocity", Value: fmt.Sprintf("%d", velocity), Unit: "m/s", Status: "NOMINAL"},
				{Channel: "throttle", Value: fmt.Sprintf("%d%%", throttle), Unit: "%", Status: "NOMINAL"},
				{Channel: "propellant", Value: fmt.Sprintf("%d%%", propellant), Unit: "%", Status: statusFor(propellant)},
				{Channel: "engine-cluster", Value: "all", Unit: "—", Status: engineStatus},
				{Channel: "comms", Value: "established", Unit: "—", Status: "NOMINAL"},
				{Channel: "payload-bay", Value: "pressurized", Unit: "—", Status: "NOMINAL"},
				{Channel: "mission-clock", Value: "T+" + elapsed(m.Date), Unit: "—", Status: "RUNNING"},
			}

			format := root.Viper.GetString("format")
			if format == output.Table {
				printTelemetryTable(m.Name, rows)
				return nil
			}
			return output.Render(os.Stdout, format, rows)
		},
	}
}

func statusFor(pct int) string {
	if pct < 20 {
		return "LOW"
	}
	return "NOMINAL"
}

func elapsed(dateStr string) string {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return "??:??:??"
	}
	d := time.Since(t)
	days := int(d.Hours() / 24)
	if days > 365 {
		return fmt.Sprintf("%dy %dd", days/365, days%365)
	}
	return fmt.Sprintf("%dd %dh", days, int(d.Hours())%24)
}

func printTelemetryTable(mission string, rows []telemetryRow) {
	fmt.Printf("\n  ╭─ TELEMETRY: %s ───────────────────────────────────────────╮\n", mission)
	fmt.Println("  │  Live feed. Values change each run. That's not a bug.          │")
	fmt.Println("  ╰────────────────────────────────────────────────────────────────╯")
	fmt.Println()
	fmt.Printf("  %-20s %-12s %-8s %s\n", "CHANNEL", "VALUE", "UNIT", "STATUS")
	fmt.Println("  ──────────────────────────────────────────────────────────────────")
	for _, r := range rows {
		fmt.Printf("  %-20s %-12s %-8s %s\n", r.Channel, r.Value, r.Unit, r.Status)
	}
	fmt.Println()
	if hasRUD(rows) {
		fmt.Println("  * RUD = Rapid Unscheduled Disassembly")
	}
}

func hasRUD(rows []telemetryRow) bool {
	for _, r := range rows {
		if r.Status == "RUD*" {
			return true
		}
	}
	return false
}
