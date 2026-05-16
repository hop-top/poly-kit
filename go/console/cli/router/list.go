package router

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

func listCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List running routellm instances",
		Long: `List running routellm server instances.

Reads PID files from the state directory and checks whether each
process is still alive. Stale PID files are cleaned up automatically.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := stateDir()
			if err != nil {
				return fmt.Errorf("resolve state dir: %w", err)
			}

			entries, err := os.ReadDir(dir)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Fprintln(
						cmd.OutOrStdout(),
						"no running instances",
					)
					return nil
				}
				return fmt.Errorf("read state dir: %w", err)
			}

			found := 0
			for _, e := range entries {
				if e.IsDir() ||
					!strings.HasSuffix(e.Name(), ".pid") {
					continue
				}

				slug := strings.TrimSuffix(e.Name(), ".pid")
				path := filepath.Join(dir, e.Name())

				pid, alive := readAndCheckPID(path)
				if !alive {
					// Stale PID file; clean up.
					_ = os.Remove(path)
					continue
				}

				found++
				fmt.Fprintf(
					cmd.OutOrStdout(),
					"%-20s pid=%d\n", slug, pid,
				)
			}

			if found == 0 {
				fmt.Fprintln(
					cmd.OutOrStdout(), "no running instances",
				)
			}
			return nil
		},
	}
	return cmd
}

// readAndCheckPID reads a PID from a file and checks if the process
// is alive. Returns the PID and whether the process exists.
func readAndCheckPID(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return pid, false
	}

	// Signal 0 checks existence without sending a signal.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return pid, false
	}
	return pid, true
}
