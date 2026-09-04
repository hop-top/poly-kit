//go:build !windows

package cmdsurface

import (
	"errors"
	"syscall"
)

// processAlive reports whether a process with pid exists. Signal 0
// delivers nothing and fails with ESRCH once the pid is gone.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
