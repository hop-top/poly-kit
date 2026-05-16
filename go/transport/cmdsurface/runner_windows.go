//go:build windows

package cmdsurface

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// applySubprocessAttrs is a no-op on Windows. The kernel-level
// "process group" concept differs from POSIX — equivalent isolation
// for supervised services is typically achieved through job objects
// or the service control manager, both of which are out of scope for
// this primitive. We still allocate SysProcAttr so callers may rely
// on it being non-nil after the call.
func applySubprocessAttrs(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
}

// killProcessTree falls back to a best-effort cmd.Process.Kill() on
// Windows. Grandchildren are NOT guaranteed to be reaped — callers
// that need transitive termination should wrap the binary in a job
// object themselves. See the package doc for the platform contract.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
