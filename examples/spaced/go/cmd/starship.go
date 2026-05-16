package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"hop.top/kit/examples/spaced/go/data"
	"hop.top/kit/go/console/cli"
)

// StarshipCmd returns the `starship` subcommand tree.
func StarshipCmd(root *cli.Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "starship",
		Short: "Starship program status and history",
	}
	cmd.AddCommand(starshipStatusCmd(root))
	cmd.AddCommand(starshipHistoryCmd(root))
	return cmd
}

func starshipStatusCmd(root *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Current Starship program status",
		RunE: func(cmd *cobra.Command, args []string) error {
			v, _ := data.FindVehicle("starship")
			fmt.Println()
			fmt.Println("  ╭─ STARSHIP PROGRAM STATUS ────────────────────────────────────────╮")
			fmt.Printf("  │  Vehicle    : %s%-50s│\n", v.Name, "")
			fmt.Printf("  │  Status     : %-53s│\n", v.Status)
			fmt.Printf("  │  Flights    : %-53s│\n", fmt.Sprintf("%d total (IFT-1 through IFT-6)", v.Flights))
			fmt.Printf("  │  Landings   : %-53s│\n", fmt.Sprintf("%d (2× Mechazilla catches)", v.Landings))
			fmt.Printf("  │  Height     : %-53s│\n", v.Height+" (tallest rocket ever flown)")
			fmt.Printf("  │  Payload    : %-53s│\n", trunc(v.Payload, 53))
			fmt.Println("  │                                                                  │")
			fmt.Println("  │  Artemis III : Starship is NASA's Human Landing System           │")
			fmt.Println("  │  Timeline    : Nominally 2026–2027 (subject to 'updates')        │")
			fmt.Println("  │  FAA Status  : Approved for IFT-6; next TBD                      │")
			fmt.Println("  │  Mechazilla  : Operational; caught boosters 2× successfully      │")
			fmt.Printf("  │  Note        : %-53s│\n", trunc(data.Pick(v.Notes), 53))
			fmt.Println("  ╰────────────────────────────────────────────────────────────────────╯")
			fmt.Println()
			return nil
		},
	}
}

func starshipHistoryCmd(root *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "history",
		Short: "Starship flight history (IFT-1 through IFT-6)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ids := []string{"ift1", "ift2", "ift3", "ift4", "ift5", "ift6"}
			fmt.Println()
			fmt.Printf("  %-10s %-14s %-12s %-10s %s\n",
				"FLIGHT", "DATE", "OUTCOME", "BOOSTER", "NOTES")
			fmt.Println("  ──────────────────────────────────────────────────────────────────────────────────────")
			for _, id := range ids {
				m, ok := data.FindMission(id)
				if !ok {
					continue
				}
				booster := "Lost"
				if m.Outcome == data.OutcomeSuccess {
					if id == "ift5" || id == "ift6" {
						booster = "Caught (Mechazilla)"
					} else {
						booster = "Splashed"
					}
				}
				fmt.Printf("  %-10s %-14s %-12s %-20s %s\n",
					m.Name, m.Date, string(m.Outcome), booster, trunc(data.Pick(m.Assessments), 45))
			}
			fmt.Println()
			fmt.Println("  Legend: RUD* = Rapid Unscheduled Disassembly (company terminology)")
			fmt.Println()
			return nil
		},
	}
}
