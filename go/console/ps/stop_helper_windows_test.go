//go:build windows

package ps_test

import (
	"os/exec"
	"testing"
)

// newHelperCmd builds a long-running child for Stop tests. On Windows
// SIGTERM is not delivered by the Go runtime, so we only support the
// "sleep" mode — Stop's escalation path is exercised via TerminateProcess
// (proc.Kill), which the test asserts via post-Stop liveness only.
func newHelperCmd(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	switch mode {
	case "sleep", "ignore-sigterm":
		// `ping` with a high count is the portable Windows "sleep N":
		// available on every supported version, no PowerShell needed.
		return exec.Command("ping", "-n", "300", "127.0.0.1")
	default:
		t.Fatalf("unknown helper mode %q", mode)
		return nil
	}
}

// newEchoCmd builds a one-shot child that prints msg and exits. The
// stdio-routing tests are POSIX-only (skipped on Windows) so this is
// just a stub that satisfies the package interface.
func newEchoCmd(t *testing.T, msg string) *exec.Cmd {
	t.Helper()
	return exec.Command("cmd", "/c", "echo "+msg)
}
