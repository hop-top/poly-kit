package ps

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// EntryFromPIDFile reads a .pid file and returns one [Entry].
//
// The file must contain a single decimal PID, optionally with
// surrounding whitespace or a trailing newline. The PID is probed
// for liveness via syscall.Kill(pid, 0); a live process yields
// [StatusRunning] while a dead one yields [StatusStopped]. The
// file mtime is used as the process start time, which is good
// enough for "process started at" displays.
//
// The returned Entry has ID set to the PID rendered as a decimal
// string so kit/console/ps render code can show it without
// caring how it was acquired.
func EntryFromPIDFile(path string) (Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, fmt.Errorf("ps: read pid file %s: %w", path, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return Entry{}, fmt.Errorf("ps: parse pid in %s: %w", path, err)
	}
	if pid <= 0 {
		return Entry{}, fmt.Errorf("ps: invalid pid %d in %s", pid, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return Entry{}, fmt.Errorf("ps: stat pid file %s: %w", path, err)
	}

	status := StatusStopped
	if IsAlive(pid) {
		status = StatusRunning
	}

	return Entry{
		ID:      strconv.Itoa(pid),
		Status:  status,
		Started: info.ModTime(),
	}, nil
}

// LoadFromPIDDir globs *.pid files in dir and returns one [Entry]
// per readable, parseable file. Files that fail to read or parse
// are silently skipped — a malformed pid file is observable as a
// missing entry, not a hard error, so a single bad file doesn't
// poison the whole listing.
func LoadFromPIDDir(dir string) ([]Entry, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.pid"))
	if err != nil {
		return nil, fmt.Errorf("ps: glob %s: %w", dir, err)
	}
	out := make([]Entry, 0, len(matches))
	for _, p := range matches {
		entry, err := EntryFromPIDFile(p)
		if err != nil {
			// Skip unparseable / unreadable pid files; surface them
			// as absences rather than failing the whole load.
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

// IsAlive reports whether pid refers to a process the calling user can
// signal. Signal 0 is the canonical "is it alive" probe on POSIX
// systems. A process owned by another user typically returns EPERM
// here, which we treat as alive — the process exists, the caller just
// can't deliver real signals to it.
//
// Returns false for pids ≤ 0 and for pids that os.FindProcess cannot
// resolve (Windows: the OS reports already-exited; POSIX: pid table
// lookup failed).
func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM means the process exists but we can't signal it.
	return errors.Is(err, syscall.EPERM)
}
