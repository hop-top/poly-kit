package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli"
)

// IpoCmd returns the `ipo` subcommand tree.
func IpoCmd(root *cli.Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ipo",
		Short: "SpaceX IPO status tracker",
	}
	cmd.AddCommand(ipoStatusCmd(root))
	return cmd
}

func ipoStatusCmd(root *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Current SpaceX IPO status",
		RunE: func(cmd *cobra.Command, args []string) error {
			quotes := []string{
				`"SpaceX will not IPO until after Starship reaches Mars"`,
				`"An IPO would add unnecessary public market pressure to our mission"`,
				`"Going public is not in SpaceX's near-term plans"`,
				`"We might spin off Starlink eventually"`,
				`"The goal is Mars, not quarterly earnings calls"`,
			}
			idx := time.Now().UnixNano() % int64(len(quotes))
			if idx < 0 {
				idx = -idx
			}

			fmt.Println()
			fmt.Println("  ╭─ IPO STATUS ──────────────────────────────────────────────────────╮")
			fmt.Println("  │                                                                     │")
			fmt.Println("  │  Company        : Space Exploration Technologies Corp. (SpaceX)     │")
			fmt.Println("  │  Ticker         : N/A                                              │")
			fmt.Println("  │  Exchange       : None                                              │")
			fmt.Println("  │  IPO Date       : TBD (probably never, see Elon quote)             │")
			fmt.Println("  │  Valuation      : ~$210B (last secondary round, 2024)              │")
			fmt.Println("  │  Funding rounds : 22+ (per Crunchbase; Elon stopped counting)      │")
			fmt.Println("  │                                                                     │")
			fmt.Printf("  │  Elon quote     : %-51s│\n", "")
			fmt.Printf("  │    %-65s│\n", quotes[idx])
			fmt.Println("  │                                                                     │")
			fmt.Println("  │  Starlink IPO   : Mentioned periodically; timeline: 'someday'      │")
			fmt.Println("  │  Mars condition : Mars must be reached first. Currently: not yet.  │")
			fmt.Println("  │                                                                     │")
			fmt.Println("  ╰─────────────────────────────────────────────────────────────────────╯")
			fmt.Println()
			fmt.Println("  Bottom line: SpaceX is private. It will remain private until Mars.")
			fmt.Println("  Mars is estimated 'this decade' (has been since 2016).")
			fmt.Println()
			return nil
		},
	}
}
