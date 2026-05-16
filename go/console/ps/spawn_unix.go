//go:build !windows

package ps

import (
	"os/exec"
	"syscall"
)

// applyDetachAttr ensures cmd.SysProcAttr requests a new process group
// so signals delivered to the parent's pgrp do not propagate to the
// child. Pre-existing SysProcAttr fields the caller set are preserved;
// only Setpgid is forced on.
func applyDetachAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
