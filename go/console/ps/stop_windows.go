//go:build windows

package ps

import (
	"errors"
	"os"
	"strings"
	"syscall"
)

// signalGraceful is best-effort on Windows. The Go runtime does not
// implement SIGTERM delivery to non-self processes — Signal() returns
// a syscall error wrapping "not supported by windows". Stop callers
// should treat that result via isUnsupportedSignal and proceed to
// Kill, which on Windows maps to TerminateProcess.
func signalGraceful(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}

// isUnsupportedSignal reports whether err is the Windows "not
// supported by windows" sentinel returned by os.Process.Signal. We
// match on text rather than a typed error because Go's runtime
// returns a plain *errors.errorString for this case.
func isUnsupportedSignal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EWINDOWS) {
		return true
	}
	return strings.Contains(err.Error(), "not supported by windows")
}
