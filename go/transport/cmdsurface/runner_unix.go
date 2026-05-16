//go:build !windows

package cmdsurface

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// applySubprocessAttrs puts the child into its own process group via
// Setpgid so signals destined for the entire group (SIGKILL on the
// negated pgid) reach grandchildren too. Pre-existing SysProcAttr
// fields the caller might have set are preserved; only Setpgid is
// forced on.
func applySubprocessAttrs(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessTree terminates the entire process group the child leads
// by sending SIGKILL to -pgid. This is the cancel path wired into
// cmd.Cancel; it runs when ctx is canceled while the subprocess is
// alive. If the pgid cannot be resolved (process already exited, or
// the pgid call failed) we fall back to killing just the process,
// which is what the default cmd.Cancel does.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		// Process already reaped, or the kernel cannot tell us its
		// group. Best-effort fall back to a per-process kill.
		if killErr := cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			return killErr
		}
		return nil
	}
	// SIGKILL the negated pgid: kernel delivers to every process in
	// the group, including the leader. ESRCH ("no such process") is
	// treated as already-gone.
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	return nil
}
