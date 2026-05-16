package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"hop.top/kit/examples/spaced/go/data"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// vehicleRow is table-renderable fleet overview.
type vehicleRow struct {
	Name        string `table:"VEHICLE"       json:"vehicle"        yaml:"vehicle"`
	Type        string `table:"TYPE"          json:"type"           yaml:"type"`
	Status      string `table:"STATUS"        json:"status"         yaml:"status"`
	Flights     int    `table:"FLIGHTS"       json:"flights"        yaml:"flights"`
	Landings    int    `table:"LANDINGS"      json:"landings"       yaml:"landings"`
	FirstFlight string `table:"FIRST FLIGHT"  json:"first_flight"   yaml:"first_flight"`
}

// FleetCmd returns the `fleet` command tree.
func FleetCmd(root *cli.Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fleet",
		Short: "Inspect the SpaceX vehicle fleet",
	}
	cmd.AddCommand(fleetListCmd(root))
	cmd.AddCommand(fleetVehicleCmd(root))
	return cmd
}

func fleetListCmd(root *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all fleet vehicles",
		RunE: func(cmd *cobra.Command, args []string) error {
			rows := make([]vehicleRow, len(data.Vehicles))
			for i, v := range data.Vehicles {
				rows[i] = vehicleRow{
					Name:        v.Name,
					Type:        v.Type,
					Status:      v.Status,
					Flights:     v.Flights,
					Landings:    v.Landings,
					FirstFlight: v.FirstFlight,
				}
			}
			return output.Render(os.Stdout, root.Viper.GetString("format"), rows)
		},
	}
}

func fleetVehicleCmd(root *cli.Root) *cobra.Command {
	var systems []string

	cmd := &cobra.Command{
		Use:   "vehicle",
		Short: "Vehicle subcommands",
	}

	inspectCmd := &cobra.Command{
		Use:   "inspect <name>",
		Short: "Inspect a vehicle in detail",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, ok := data.FindVehicle(args[0])
			if !ok {
				fmt.Fprintf(os.Stderr, "vehicle not found: %s\n", args[0])
				return fmt.Errorf("vehicle not found: %s", args[0])
			}

			fmt.Println()
			fmt.Printf("  ╭─ VEHICLE: %s ─────────────────────────────────────────────────╮\n", v.Name)
			vrow := func(label, value string) {
				fmt.Printf("  │  %-14s %-49s│\n", label, trunc(value, 49))
			}
			vrow("Type", v.Type)
			vrow("Status", v.Status)
			vrow("First Flight", v.FirstFlight)
			vrow("Height", v.Height)
			vrow("Payload", v.Payload)
			vrow("Flights", fmt.Sprintf("%d", v.Flights))
			vrow("Landings", fmt.Sprintf("%d", v.Landings))
			vrow("Note", data.Pick(v.Notes))
			vrow("Elon quote", data.Pick(v.ElonQuotes))
			fmt.Println("  ╰────────────────────────────────────────────────────────────────────╯")

			// Systems filter
			systemFilter := systems
			if len(systemFilter) > 0 {
				fmt.Println()
				fmt.Println("  SYSTEMS")
				fmt.Println("  ────────────────────────────────────────────────────────────────────")
				for _, raw := range systemFilter {
					for _, s := range strings.Split(raw, ",") {
						s = strings.TrimSpace(s)
						if s == "" {
							continue
						}
						key := strings.ToLower(s)
						sys, ok := v.Systems[key]
						if !ok {
							fmt.Printf("  %-16s (unknown system)\n", s)
							continue
						}
						fmt.Printf("  %-16s [%s] %s\n", sys.Name, sys.Status, sys.Description)
					}
				}
			} else if len(v.Systems) > 0 {
				fmt.Println()
				fmt.Println("  SYSTEMS  (use --systems to filter)")
				fmt.Println("  ────────────────────────────────────────────────────────────────────")
				for _, sys := range v.Systems {
					fmt.Printf("  %-16s [%s] %s\n", sys.Name, sys.Status, trunc(sys.Description, 60))
				}
			}
			fmt.Println()
			return nil
		},
	}

	inspectCmd.Flags().StringArrayVar(&systems, "systems", nil, "Systems to inspect (comma-list)")
	cmd.AddCommand(inspectCmd)
	return cmd
}
