package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"hop.top/kit/examples/spaced/go/data"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/runtime/bus"
)

// DaemonCmd returns the `daemon` subcommand tree.
func DaemonCmd(root *cli.Root, b bus.Bus) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage background controversy processes",
		Long:  "Controversies are long-running processes. You can list them, inspect them, and attempt to stop them.",
	}
	cmd.AddCommand(daemonListCmd(root))
	cmd.AddCommand(daemonStatusCmd(root))
	cmd.AddCommand(daemonStopCmd(root, b))
	return cmd
}

func daemonListCmd(root *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all active daemons",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println()
			fmt.Println("  ╭─ ACTIVE DAEMONS ────────────────────────────────────────────────╮")
			fmt.Println("  │  These processes are running in the background. Allegedly.       │")
			fmt.Println("  ╰─────────────────────────────────────────────────────────────────╯")
			fmt.Println()
			fmt.Printf("  %-4s %-35s %-10s %s\n", "ID", "DAEMON", "STATUS", "SINCE")
			fmt.Println("  ──────────────────────────────────────────────────────────────────────")
			for i, d := range data.Daemons {
				fmt.Printf("  %03d  %-35s %-10s %s\n", i+1, d.Name, d.Status, d.Since)
			}
			fmt.Println()
			fmt.Println("  Use 'spaced daemon status <id>' for media references.")
			fmt.Println("  Use 'spaced daemon stop <id>' to attempt termination.")
			fmt.Println("  Good luck.")
			fmt.Println()
			return nil
		},
	}
}

func daemonStatusCmd(root *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "status <id>",
		Short: "Show daemon status and media references",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, ok := data.FindDaemon(args[0])
			if !ok {
				fmt.Fprintf(os.Stderr, "daemon not found: %s\n", args[0])
				return fmt.Errorf("daemon not found: %s", args[0])
			}

			fmt.Println()
			fmt.Printf("  ╭─ DAEMON: %s ─────────────────────────────────────────────╮\n", d.Name)
			fmt.Printf("  │  Status     : %-53s│\n", d.Status)
			fmt.Printf("  │  Since      : %-53s│\n", d.Since)
			fmt.Println("  │                                                                  │")
			// Word-wrap description to ~62 chars
			words := splitWords(d.Description, 62)
			for _, line := range words {
				fmt.Printf("  │  %-66s│\n", line)
			}
			fmt.Println("  ╰────────────────────────────────────────────────────────────────────╯")

			if len(d.References) > 0 {
				fmt.Println()
				fmt.Printf("  %-25s %-25s %-12s %s\n", "SOURCE", "AUTHOR", "DATE", "SUMMARY")
				fmt.Println("  ──────────────────────────────────────────────────────────────────────────────────────────────────────────────")
				for _, ref := range d.References {
					fmt.Printf("  %-25s %-25s %-12s %s\n",
						trunc(ref.Source, 25),
						trunc(ref.Author, 25),
						ref.Date,
						trunc(ref.Summary, 65))
				}
			}
			fmt.Println()
			return nil
		},
	}
}

func daemonStopCmd(root *cli.Root, b bus.Bus) *cobra.Command {
	var stopAll bool

	cmd := &cobra.Command{
		Use:   "stop [id]",
		Short: "Attempt to stop a daemon",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cli.SafetyGuard(cmd, cli.SafetyDangerous); err != nil {
				return err
			}
			ctx := cmd.Context()
			if stopAll {
				total := len(data.Daemons)
				fmt.Println()
				fmt.Printf("  ✗ STOP FAILED: all daemons (%d/%d)\n", total, total)
				fmt.Printf("  Stopped             : 0\n")
				fmt.Printf("  Still running       : %d\n", total)
				fmt.Printf("  New daemons spawned : 1\n")
				fmt.Println("    → musk-response-to-this-cli  [RUNNING since just now]")
				fmt.Println()
				fmt.Println("  Suggestion: try `spaced daemon stop --all` again.")
				fmt.Println("  Historical note: this has never worked for anyone.")
				fmt.Println()
				return nil
			}

			if len(args) == 0 {
				return fmt.Errorf("specify a daemon id or use --all")
			}

			d, ok := data.FindDaemon(args[0])
			if !ok {
				fmt.Fprintf(os.Stderr, "daemon not found: %s\n", args[0])
				return fmt.Errorf("daemon not found: %s", args[0])
			}

			_ = b.Publish(ctx, bus.NewEvent(
				"kit.spaced.daemon.stopped", "spaced",
				map[string]any{"daemon": args[0]},
			))

			fmt.Println()
			fmt.Printf("  ✗ STOP FAILED: %s\n", d.Name)
			fmt.Printf("  %s\n", d.StopMessage)
			fmt.Println()

			return nil
		},
	}

	cmd.Flags().BoolVar(&stopAll, "all", false, "Attempt to stop all daemons (spoiler: won't work)")
	cmd.Flags().Bool("force", false, "Skip safety confirmation")
	return cmd
}

// splitWords wraps s into lines of max width chars.
func splitWords(s string, width int) []string {
	var lines []string
	var current string
	for _, word := range splitOnSpaces(s) {
		if len(current)+len(word)+1 > width && current != "" {
			lines = append(lines, current)
			current = word
		} else if current == "" {
			current = word
		} else {
			current += " " + word
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func splitOnSpaces(s string) []string {
	var words []string
	var word string
	for _, r := range s {
		if r == ' ' {
			if word != "" {
				words = append(words, word)
				word = ""
			}
		} else {
			word += string(r)
		}
	}
	if word != "" {
		words = append(words, word)
	}
	return words
}
