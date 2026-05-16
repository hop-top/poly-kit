package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"hop.top/kit/examples/spaced/go/data"
	"hop.top/kit/go/console/cli"
)

// CompetitorCmd returns the `competitor` command tree.
func CompetitorCmd(root *cli.Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "competitor",
		Short: "Compare SpaceX against its competitors",
	}
	cmd.AddCommand(competitorCompareCmd(root))
	return cmd
}

func competitorCompareCmd(root *cli.Root) *cobra.Command {
	var metrics []string

	cmd := &cobra.Command{
		Use:   "compare <name>",
		Short: "Compare SpaceX vs a named competitor",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ok := data.FindCompetitor(args[0])
			if !ok {
				fmt.Fprintf(os.Stderr, "competitor not found: %s\n", args[0])
				fmt.Fprintln(os.Stderr, "Available: boeing, blue-origin, virgin-galactic, ula, roscosmos")
				return fmt.Errorf("competitor not found: %s", args[0])
			}

			fmt.Println()
			fmt.Printf("  ╭─ COMPETITOR: %s ─────────────────────────────────────────╮\n", c.Name)
			crow := func(label, value string) {
				fmt.Printf("  │  %-14s %-52s│\n", label, trunc(value, 52))
			}
			crow("Founded", c.Founded)
			crow("CEO", c.CEO)
			crow("Status", c.Status)
			fmt.Println("  │                                                                  │")
			fmt.Println("  │  HIGHLIGHTS                                                      │")
			for _, h := range c.Highlights {
				fmt.Printf("  │    · %-62s│\n", trunc(h, 62))
			}
			fmt.Println("  │                                                                  │")
			fmt.Printf("  │  Verdict : %-56s│\n", trunc(c.Verdict, 56))
			fmt.Println("  ╰────────────────────────────────────────────────────────────────────╯")

			// Metrics comparison
			fmt.Println()

			selectedMetrics := c.Metrics
			if len(metrics) > 0 {
				selectedMetrics = make(map[string]data.CompetitorMetric)
				for _, raw := range metrics {
					for _, key := range strings.Split(raw, ",") {
						k := strings.TrimSpace(strings.ToLower(key))
						if k == "" {
							continue
						}
						if m, ok := c.Metrics[k]; ok {
							selectedMetrics[k] = m
						}
					}
				}
			}

			if len(selectedMetrics) > 0 {
				fmt.Printf("  %-30s %-18s %-18s %s\n", "METRIC", "SPACEX", c.Name, "WINNER")
				fmt.Println("  ────────────────────────────────────────────────────────────────────────────────────")
				for _, m := range selectedMetrics {
					fmt.Printf("  %-30s %-18s %-18s %s\n",
						trunc(m.Label, 30),
						trunc(m.SpaceX, 18),
						trunc(m.Them, 18),
						m.Winner)
				}
				fmt.Println()
			}

			if len(c.ElonQuotes) > 0 {
				fmt.Printf("  Elon on %s:\n", c.Name)
				for _, q := range c.ElonQuotes {
					fmt.Printf("    %s\n", q)
				}
				fmt.Println()
			}

			return nil
		},
	}

	cmd.Flags().StringArrayVar(&metrics, "metric", nil, "Metrics to compare (comma-list)")
	return cmd
}
