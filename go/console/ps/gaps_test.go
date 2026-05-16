package ps_test

// Gap tests for `hop.top/kit/go/console/ps`. Surfaced by the dpkms
// review (dpkms ps is hand-rolled in internal/pidfile/ rather than
// using kit's ps primitive).

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/ps"
)

// Gap: kit/console/ps lacks a PID-file → Entry loader.
//
// The package ships Entry / Status / Progress / render helpers, but
// adopters with on-disk pid files (one per long-running process)
// have no helper that reads a directory of .pid files and returns
// []Entry. dpkms reimplemented this in internal/pidfile/ rather
// than feeding ps.Entry values to ps's render.
//
// Desired API:
//
//	entries, err := ps.LoadFromPIDDir("/var/run/dpkms")
//	// each .pid file → one Entry with PID, started-at, status
//	// inferred from process liveness (signal 0 probe).
//
// Or, more conservative:
//
//	e, err := ps.EntryFromPIDFile(path)
//
// either signature would let dpkms drop pidfile/ and call
// ps.Render(entries, ...) directly.
func TestGap_PSLoadFromPIDFile_Missing(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "self.pid")
	pid := os.Getpid()
	require.NoError(t, os.WriteFile(pidPath, []byte(strconv.Itoa(pid)+"\n"), 0o600))

	entry, err := ps.EntryFromPIDFile(pidPath)
	require.NoError(t, err)
	require.Equal(t, strconv.Itoa(pid), entry.ID)
	require.Equal(t, ps.StatusRunning, entry.Status)
	require.False(t, entry.Started.IsZero())

	// LoadFromPIDDir should also find it.
	entries, err := ps.LoadFromPIDDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, entry.ID, entries[0].ID)
}

// pin: Entry is the type a pid-file loader would return.
var _ = ps.Entry{}

// IsAlive is the exported liveness probe required by callers that hold a
// pid (e.g. from their own state) without round-tripping through a pid
// file. Before US-0008 it lived as the unexported isProcessAlive, which
// pushed adopters (aps voice backend, dpkms) to duplicate it.
func TestIsAlive_Self(t *testing.T) {
	require.True(t, ps.IsAlive(os.Getpid()))
}

func TestIsAlive_NonPositive(t *testing.T) {
	require.False(t, ps.IsAlive(0))
	require.False(t, ps.IsAlive(-1))
}

func TestIsAlive_DefinitelyDead(t *testing.T) {
	// Pid 1 is init/launchd — always alive on a running system, so we
	// can't use it as the dead case. Pick a pid that's almost certainly
	// not in use: the largest legal POSIX pid is platform-dependent but
	// 0x7fffffff is safely out of range on all common systems.
	require.False(t, ps.IsAlive(0x7fffffff))
}
