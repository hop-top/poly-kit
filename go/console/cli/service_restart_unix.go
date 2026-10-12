//go:build unix

package cli

import (
	"errors"
	"syscall"
)

// reexecSupported reports whether this platform can replace the
// process image in place. A var so tests can take the other branch.
var reexecSupported = true

func reexec(path string, args, env []string) error {
	return syscall.Exec(path, args, env)
}

// processAlive reports whether pid names a live process. EPERM means
// it exists under another user.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
