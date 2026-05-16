package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"hop.top/kit/examples/spaced/go/data"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// missionRow is the table-renderable form of a mission.
type missionRow struct {
	Mission    string `table:"MISSION"      json:"mission"      yaml:"mission"`
	Vehicle    string `table:"VEHICLE"      json:"vehicle"      yaml:"vehicle"`
	Date       string `table:"DATE"         json:"date"         yaml:"date"`
	Outcome    string `table:"OUTCOME"      json:"outcome"      yaml:"outcome"`
	MarketMood string `table:"MARKET MOOD"  json:"market_mood"  yaml:"market_mood"`
}

// MissionCmd returns the `mission` subcommand tree.
func MissionCmd(root *cli.Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mission",
		Short: "Query mission history",
		Long:  "Browse, inspect, and search the SpaceX mission archive.",
	}
	cmd.AddCommand(missionListCmd(root))
	cmd.AddCommand(missionInspectCmd(root))
	cmd.AddCommand(missionSearchCmd(root))
	return cmd
}

func missionListCmd(root *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all missions",
		RunE: func(cmd *cobra.Command, args []string) error {
			format := root.Viper.GetString("format")
			rows := make([]missionRow, 0, len(data.Missions))
			for _, m := range data.Missions {
				rows = append(rows, missionRow{
					Mission:    m.Name,
					Vehicle:    m.Vehicle,
					Date:       m.Date,
					Outcome:    string(m.Outcome),
					MarketMood: data.Pick(m.MarketMoods),
				})
			}
			if format == output.Table {
				printMissionTable(rows)
				return nil
			}
			return output.Render(os.Stdout, format, rows, output.WithProvenance(output.Metadata{
				Source:    "local",
				FetchedAt: time.Now().UTC(),
				Method:    "static",
			}))
		},
	}
}

func printMissionTable(rows []missionRow) {
	const divider = "  ────────────────────────────────────────────────────────────────────"
	fmt.Printf("  %-20s %-16s %-12s %-10s %s\n",
		"MISSION", "VEHICLE", "DATE", "OUTCOME", "MARKET MOOD")
	fmt.Println(divider)
	for _, r := range rows {
		fmt.Printf("  %-20s %-16s %-12s %-10s %s\n",
			r.Mission, r.Vehicle, r.Date, r.Outcome, r.MarketMood)
	}
	fmt.Println()
	fmt.Println("  * RUD = Rapid Unscheduled Disassembly  (company terminology, not ours)")
}

func missionInspectCmd(root *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <name>",
		Short: "Inspect a mission in detail",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			m, ok := data.FindMission(name)
			if !ok {
				return &cli.CorrectedError{
					Code:         "NOT_FOUND",
					Message:      fmt.Sprintf("mission not found: %s", name),
					Cause:        fmt.Sprintf("no mission matches %q", name),
					Fix:          "spaced mission list",
					Alternatives: []string{"spaced mission search " + name},
					Retryable:    false,
				}
			}
			printMissionInspect(m)
			return nil
		},
	}
}

func printMissionInspect(m *data.Mission) {
	title := fmt.Sprintf("MISSION: %s", m.Name)
	width := 65
	border := strings.Repeat("─", width-len("╭─ ")-len(" ──")+len(title))

	fmt.Printf("  ╭─ %s %s╮\n", title, border[:max(0, width-4-len(title))])
	row := func(label, value string) {
		fmt.Printf("  │  %-14s %-47s│\n", label, trunc(value, 47))
	}
	row("Vehicle", m.Vehicle)
	row("Date", m.Date+" UTC")
	row("Orbit", m.Orbit)
	row("Payload", m.Payload)
	row("Outcome", string(m.Outcome))
	if m.Passenger != "" {
		row("Passenger", m.Passenger)
	}
	if m.Playing != "" {
		row("Playing", m.Playing)
	}
	if m.Location != "" {
		row("Location", m.Location)
	}
	if len(m.ElonQuotes) > 0 {
		row("Elon quote", data.Pick(m.ElonQuotes))
	}
	if len(m.Assessments) > 0 {
		row("Assessment", data.Pick(m.Assessments))
	}
	if len(m.Notes) > 0 {
		row("Note", data.Pick(m.Notes))
	}
	fmt.Printf("  ╰%s╯\n", strings.Repeat("─", width-2))
}

func missionSearchCmd(root *cli.Root) *cobra.Command {
	var query string
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Search missions by name, vehicle, or payload",
		RunE: func(cmd *cobra.Command, args []string) error {
			if query == "" {
				return fmt.Errorf("--query is required")
			}
			results := data.SearchMissions(query)
			if len(results) == 0 {
				fmt.Printf("  No missions found matching %q\n", query)
				return nil
			}
			format := root.Viper.GetString("format")
			rows := make([]missionRow, 0, len(results))
			for _, m := range results {
				rows = append(rows, missionRow{
					Mission:    m.Name,
					Vehicle:    m.Vehicle,
					Date:       m.Date,
					Outcome:    string(m.Outcome),
					MarketMood: data.Pick(m.MarketMoods),
				})
			}
			if format == output.Table {
				printMissionTable(rows)
				return nil
			}
			return output.Render(os.Stdout, format, rows)
		},
	}
	cmd.Flags().StringVar(&query, "query", "", "Search term (name, vehicle, or payload)")
	return cmd
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
