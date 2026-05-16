package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli"
)

// ElonCmd returns the `elon` subcommand tree.
func ElonCmd(root *cli.Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "elon",
		Short: "Elon Musk current status",
	}
	cmd.AddCommand(elonStatusCmd(root))
	return cmd
}

func elonStatusCmd(root *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Display Elon's current operational status",
		RunE: func(cmd *cobra.Command, args []string) error {
			statuses := []string{
				"TWEETING",
				"RUNNING_DOGE",
				"AT_BOCA_CHICA",
				"ACQUIRING_SOMETHING",
				"REBRANDING_SOMETHING",
				"LITIGATING_WITH_SEC",
				"LAUNCHING_ROCKET",
				"POSTING_MEMES",
			}
			idx := time.Now().UnixNano() % int64(len(statuses))
			if idx < 0 {
				idx = -idx
			}
			status := statuses[idx]

			roles := []string{
				"CEO, Tesla",
				"CEO, SpaceX",
				"Owner, X (formerly Twitter)",
				"Head, DOGE",
				"Founder, Neuralink",
				"Founder, The Boring Company",
				"Aspirant, Mars colonist",
			}

			fmt.Println()
			fmt.Println("  ╭─ ELON STATUS ─────────────────────────────────────────────────────╮")
			fmt.Printf("  │  Name        : %-53s│\n", "Elon Reeve Musk")
			fmt.Printf("  │  Status      : %-53s│\n", status)
			fmt.Println("  │  Handle      : @elonmusk (on X, which he owns, hence the handle)   │")
			fmt.Println("  │  Net Worth   : Fluctuating (check Bloomberg; it changes hourly)     │")
			fmt.Println("  │  Roles       :                                                      │")
			for _, r := range roles {
				fmt.Printf("  │    · %-63s│\n", r)
			}
			fmt.Println("  │                                                                     │")
			fmt.Println("  │  Daemons     : 8 running (see: spaced daemon list)                 │")
			fmt.Println("  │  Active CEOs : 3 simultaneously (verified)                         │")
			fmt.Println("  │  Tweets/day  : Classified                                          │")
			fmt.Println("  ╰─────────────────────────────────────────────────────────────────────╯")
			fmt.Println()
			fmt.Println("  Not a real-time feed. For current status, check X.com.")
			fmt.Println("  He is probably posting right now.")
			fmt.Println()
			return nil
		},
	}
}
