//go:build !unix

package cli

// reexecSupported reports whether this platform can replace the
// process image in place: without exec(2) it cannot, and `service
// restart` refuses rather than emulate it with a new pid.
var reexecSupported = false

func reexec(string, []string, []string) error {
	return errReexecUnsupported
}

// processAlive is never consulted where restart is refused.
func processAlive(int) bool { return true }
