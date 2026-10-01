package router

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	kitcli "hop.top/kit/go/console/cli"
)

// waitForExit polls the process until it is no longer running, up to 10s.
func waitForExit(proc *os.Process) {
	for i := 0; i < 20; i++ {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return // process gone
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func stopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop [PID|slug]",
		Short: "Stop a running routellm server",
		Long: `Stop a running routellm server by PID or slug.

When given a numeric argument, it is treated as a PID directly.
When given a string, it is treated as a slug and the PID is read
from the corresponding PID file in the state directory.
With no argument, the "default" slug is used.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, pidFile, err := resolvePID(args)
			if err != nil {
				return err
			}

			proc, err := os.FindProcess(pid)
			if err != nil {
				return fmt.Errorf("find process %d: %w", pid, err)
			}
			if kitcli.IsDryRun(cmd) {
				return planStop(cmd, proc, pid, pidFile)
			}

			if err := proc.Signal(syscall.SIGTERM); err != nil {
				return fmt.Errorf(
					"send SIGTERM to %d: %w", pid, err,
				)
			}

			fmt.Fprintf(
				cmd.OutOrStdout(),
				"sent SIGTERM to pid %d\n", pid,
			)

			// Wait for process to exit before removing PID file.
			waitForExit(proc)

			if pidFile != "" {
				_ = os.Remove(pidFile)
			}

			return nil
		},
	}
	// Terminates a process — by slug, or any PID named — so it asks
	// first.
	kitcli.SetSideEffect(cmd, kitcli.SideEffectDestructiveLocal)
	return cmd
}

// planStop is router stop under --dry-run: the process must be there
// to signal, as for the real stop, and is left running.
func planStop(cmd *cobra.Command, proc *os.Process, pid int, pidFile string) error {
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return fmt.Errorf("process %d: %w", pid, err)
	}
	plan := kitcli.Plan{
		Args: map[string]any{"pid": pid},
		Effects: []kitcli.Effect{{Kind: "delete", Target: "pid:" + strconv.Itoa(pid),
			Reversible: false, Detail: "send SIGTERM and wait for exit"}},
	}
	if pidFile != "" {
		plan.Effects = append(plan.Effects, kitcli.Effect{Kind: "delete",
			Target: pidFile, Reversible: false, Detail: "remove the pid file"})
	}
	return kitcli.RenderPlan(cmd, plan)
}

// resolvePID determines the target PID from arguments.
// Returns the PID, the PID file path (if any), and an error.
func resolvePID(args []string) (int, string, error) {
	target := "default"
	if len(args) > 0 {
		target = args[0]
	}

	// Try as numeric PID first.
	if pid, err := strconv.Atoi(target); err == nil {
		return pid, "", nil
	}

	// Treat as slug — read PID file.
	path, err := pidFilePath(target)
	if err != nil {
		return 0, "", fmt.Errorf("resolve pid path: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return 0, "", fmt.Errorf(
			"read pid file for slug %q: %w", target, err,
		)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, "", fmt.Errorf(
			"invalid pid in %s: %w", path, err,
		)
	}
	return pid, path, nil
}
