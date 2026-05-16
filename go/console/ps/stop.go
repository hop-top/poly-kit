package ps

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// pollInterval is how often Stop re-checks IsAlive while waiting for a
// child to exit on its own after the first signal. 50ms is the same
// cadence aps's hand-roll used; tight enough to keep Stop responsive
// under short grace windows but coarse enough to avoid a busy loop.
const pollInterval = 50 * time.Millisecond

// Stop terminates the process referenced by entry, escalating from a
// graceful signal (SIGTERM on POSIX, Kill on Windows) to a hard kill if
// the process is still alive after grace expires.
//
// Stop is idempotent: it returns nil if the process is already gone, if
// the entry's PID is missing/invalid, or if the OS reports the target
// has exited between probes. Calling it twice in a row is safe.
//
// Refuses to act on the calling process — Stop on os.Getpid() returns
// an error rather than killing the host.
//
// Stop does NOT remove a PID file. Callers that own a PID file should
// remove it after Stop returns; that policy belongs to the caller, not
// the primitive.
func Stop(entry Entry, grace time.Duration) error {
	pid, err := strconv.Atoi(strings.TrimSpace(entry.ID))
	if err != nil {
		// No parseable pid — nothing to stop, nothing to error on.
		return nil
	}
	if pid <= 0 {
		return nil
	}
	if pid == os.Getpid() {
		return fmt.Errorf("ps: refusing to Stop self (pid %d)", pid)
	}

	if !IsAlive(pid) {
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		// On POSIX FindProcess never fails; on Windows a missing pid
		// returns an error. Either way: nothing to terminate.
		return nil
	}

	// Phase 1: graceful signal.
	if err := signalGraceful(proc); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		// On windows the graceful signal may be unsupported; treat
		// that as "skip straight to Kill" rather than an error.
		if !isUnsupportedSignal(err) {
			return fmt.Errorf("ps: stop pid %d: graceful signal: %w", pid, err)
		}
	}

	// Phase 2: poll until grace expires.
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !IsAlive(pid) {
			return nil
		}
		// Sleep for the smaller of pollInterval or remaining grace so
		// a tiny grace doesn't overshoot.
		remaining := time.Until(deadline)
		sleep := pollInterval
		if remaining < sleep {
			sleep = remaining
		}
		if sleep <= 0 {
			break
		}
		time.Sleep(sleep)
	}
	if !IsAlive(pid) {
		return nil
	}

	// Phase 3: hard kill.
	if err := proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("ps: stop pid %d: kill: %w", pid, err)
	}
	return nil
}
