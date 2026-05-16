package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"hop.top/kit/examples/spaced/go/data"
	"hop.top/kit/go/console/cli"
)

// CountdownCmd returns the `countdown <mission>` command.
func CountdownCmd(root *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "countdown <mission>",
		Short: "Show countdown status for a mission",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, ok := data.FindMission(args[0])
			if !ok {
				fmt.Fprintf(os.Stderr, "mission not found: %s\n", args[0])
				return fmt.Errorf("mission not found: %s", args[0])
			}

			t, err := time.Parse("2006-01-02", m.Date)
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid mission date: %v\n", err)
				return err
			}

			now := time.Now().UTC()
			diff := t.Sub(now)

			fmt.Println()
			fmt.Printf("  ╭─ COUNTDOWN: %s\n", m.Name)
			fmt.Printf("  │  Vehicle   : %s\n", m.Vehicle)
			fmt.Printf("  │  T-0       : %s 00:00:00 UTC\n", m.Date)
			fmt.Printf("  │  Now       : %s\n", now.Format("2006-01-02 15:04:05 UTC"))

			if diff > 0 {
				days := int(diff.Hours()) / 24
				hours := int(diff.Hours()) % 24
				mins := int(diff.Minutes()) % 60
				secs := int(diff.Seconds()) % 60
				fmt.Printf("  │  T-minus   : %dd %02dh %02dm %02ds\n", days, hours, mins, secs)
				fmt.Println("  │  Status    : HOLD — awaiting weather + FAA approval + Elon's tweets")
			} else {
				elapsed := now.Sub(t)
				days := int(elapsed.Hours()) / 24
				fmt.Printf("  │  T+        : %d days ago\n", days)
				fmt.Printf("  │  Outcome   : %s\n", m.Outcome)
				fmt.Printf("  │  Note      : %s\n", data.Pick(m.Notes))
				fmt.Println("  │  Status    : COMPLETE — we can't rewind time, but we can query it")
			}
			fmt.Println("  ╰────────────────────────────────────────────────────────")
			fmt.Println()

			return nil
		},
	}
}
