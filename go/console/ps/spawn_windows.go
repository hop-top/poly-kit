//go:build windows

package ps

import (
	"os/exec"
	"syscall"
)

// applyDetachAttr is a no-op on Windows: the kernel-level concept of
// process groups differs and "detached service" semantics belong to the
// service control manager. We still allocate a SysProcAttr so callers
// can rely on cmd.SysProcAttr being non-nil after the call.
func applyDetachAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
}
