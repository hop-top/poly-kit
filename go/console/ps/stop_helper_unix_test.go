//go:build !windows

package ps_test

import (
	"os/exec"
	"testing"
)

// newHelperCmd builds a long-running child appropriate to the
// requested mode. We use /bin/sh constructs rather than re-execing the
// Go test binary so the child inherits standard POSIX signal handling
// — Go's testing package installs a SIGTERM trap that would mask the
// graceful-exit path we want to verify.
func newHelperCmd(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	switch mode {
	case "sleep":
		// Plain sleep: exits on SIGTERM with the default disposition.
		return exec.Command("/bin/sh", "-c", "sleep 30")
	case "ignore-sigterm":
		// Trap SIGTERM and re-loop — only SIGKILL terminates this.
		// The trap handler is empty so the signal is swallowed.
		return exec.Command("/bin/sh", "-c", "trap '' TERM; while :; do sleep 1; done")
	default:
		t.Fatalf("unknown helper mode %q", mode)
		return nil
	}
}

// newEchoCmd builds a one-shot child that prints msg and exits. Used by
// stdio-routing tests where we want a fast, deterministic exit path.
func newEchoCmd(t *testing.T, msg string) *exec.Cmd {
	t.Helper()
	return exec.Command("/bin/sh", "-c", "printf '%s\\n' \""+msg+"\"")
}
