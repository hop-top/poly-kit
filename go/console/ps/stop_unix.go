//go:build !windows

package ps

import (
	"os"
	"syscall"
)

// signalGraceful delivers the platform's graceful-stop signal. On
// POSIX systems that is SIGTERM, the canonical "please exit" request.
func signalGraceful(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}

// isUnsupportedSignal reports whether err means "this OS does not deliver
// real signals". Always false on POSIX — SIGTERM is universally supported.
func isUnsupportedSignal(_ error) bool { return false }
