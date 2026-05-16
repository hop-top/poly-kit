package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"hop.top/kit/go/core/xdg"
)

// SyncCmd returns the `sync` command group for managing replication remotes.
func SyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Manage sync remotes",
		Long:  "Add, remove, and inspect replication remotes for mission data.",
	}
	cmd.AddCommand(syncAddCmd())
	cmd.AddCommand(syncRemoveCmd())
	cmd.AddCommand(syncStatusCmd())
	return cmd
}

func syncAddCmd() *cobra.Command {
	var mode string
	cmd := &cobra.Command{
		Use:   "add <name> <url>",
		Short: "Add a sync remote",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			name, url := args[0], args[1]
			line := fmt.Sprintf("%s %s %s\n", name, url, mode)
			if err := appendRemotesFile(line); err != nil {
				return err
			}
			fmt.Printf("  Remote added: %s → %s (mode: %s)\n", name, url, mode)
			return nil
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "both", "Sync mode: push, pull, or both")
	return cmd
}

func syncRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a sync remote",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			lines, err := readRemotesFile()
			if err != nil {
				return err
			}
			var kept []string
			for _, l := range lines {
				if !strings.HasPrefix(l, name+" ") {
					kept = append(kept, l)
				}
			}
			if err := writeRemotesFile(kept); err != nil {
				return err
			}
			fmt.Printf("  Remote removed: %s\n", name)
			return nil
		},
	}
}

func syncStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show configured remotes",
		RunE: func(_ *cobra.Command, _ []string) error {
			lines, err := readRemotesFile()
			if err != nil || len(lines) == 0 {
				fmt.Println("  No remotes configured.")
				return nil
			}
			fmt.Printf("  %-12s %-40s %s\n", "NAME", "URL", "MODE")
			fmt.Println("  " + strings.Repeat("─", 60))
			for _, l := range lines {
				parts := strings.Fields(l)
				if len(parts) >= 3 {
					fmt.Printf("  %-12s %-40s %s\n", parts[0], parts[1], parts[2])
				}
			}
			return nil
		},
	}
}

func remotesPath() string {
	dir := xdg.MustEnsure(xdg.DataDir("spaced"))
	return filepath.Join(dir, "remotes")
}

func appendRemotesFile(line string) error {
	f, err := os.OpenFile(remotesPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}

func readRemotesFile() ([]string, error) {
	raw, err := os.ReadFile(remotesPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

func writeRemotesFile(lines []string) error {
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	return os.WriteFile(remotesPath(), []byte(content), 0644)
}
