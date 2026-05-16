package ps

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// pidFileMode is the mode applied to PID files written by WritePIDFile.
// 0600 keeps the file readable only by its owner — PID files in shared
// runtime dirs (e.g. /var/run, $XDG_RUNTIME_DIR) should not leak the
// running user's process ID list to other accounts.
const pidFileMode os.FileMode = 0o600

// pidDirMode is the mode applied to a parent directory created on demand
// by WritePIDFile. Matches the XDG runtime convention used by aps.
const pidDirMode os.FileMode = 0o700

// WritePIDFile writes entry's PID to path atomically.
//
// The file is created via write-to-temp-then-rename so concurrent
// readers (e.g. another invocation calling [EntryFromPIDFile]) never
// observe a partially written file. The parent directory is created
// with mode 0700 if it does not already exist. The PID file itself is
// written with mode 0600.
//
// The on-disk format is the decimal PID followed by a single newline,
// matching what [EntryFromPIDFile] parses. Additional Entry fields
// (Worker, Scope, Track, Progress, ...) are not persisted — they are
// runtime-derived and should be set by the Provider that surfaces the
// entry, not the spawn-time writer.
//
// entry.ID must be a non-empty decimal PID string; any other format is
// rejected so a misconfigured caller fails loudly rather than producing
// an unparseable file.
func WritePIDFile(path string, entry Entry) error {
	pid, err := strconv.Atoi(strings.TrimSpace(entry.ID))
	if err != nil {
		return fmt.Errorf("ps: write pid file %s: parse entry.ID: %w", path, err)
	}
	if pid <= 0 {
		return fmt.Errorf("ps: write pid file %s: invalid pid %d", path, pid)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, pidDirMode); err != nil {
		return fmt.Errorf("ps: write pid file %s: mkdir parent: %w", path, err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("ps: write pid file %s: create temp: %w", path, err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if anything below fails before rename succeeds.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := fmt.Fprintf(tmp, "%d\n", pid); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("ps: write pid file %s: write body: %w", path, err)
	}
	if err := tmp.Chmod(pidFileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("ps: write pid file %s: chmod temp: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("ps: write pid file %s: close temp: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("ps: write pid file %s: rename: %w", path, err)
	}
	return nil
}
