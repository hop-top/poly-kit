package ps_test

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/ps"
)

// newHelperCmd is implemented per-platform; see stop_helper_*_test.go.
//   - POSIX uses /bin/sh + sleep so default SIGTERM handling fires.
//   - Windows uses ping -n as a portable long-running stand-in.
//
// The returned Cmd has been configured but not started.

// startHelper launches a long-running child process suitable for
// exercising ps.Stop. Modes are selected via the mode argument and
// realized by the platform-specific helper backend (see
// stop_helper_*_test.go).
//
//   - "sleep"          — long sleep, default signal handlers
//   - "ignore-sigterm" — installs a SIGTERM trap that swallows the
//     signal, forcing Stop to escalate to Kill (POSIX only)
//
// The returned Cmd has a background reaper installed: the test harness
// waits on the child in a goroutine so that a killed-but-unreaped
// zombie does not register as alive in subsequent IsAlive probes.
// In production, this responsibility belongs to whoever spawned the
// child (typically [SpawnDetached] in this package).
func startHelper(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	cmd := newHelperCmd(t, mode)
	require.NoError(t, cmd.Start())
	done := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	return cmd
}

// waitGone polls IsAlive until it returns false or the deadline expires.
// Used after Stop to absorb the brief gap between the parent reaping the
// child and the kernel removing the pid from the process table.
func waitGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !ps.IsAlive(pid) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return !ps.IsAlive(pid)
}

func TestStop_GracefulExit(t *testing.T) {
	cmd := startHelper(t, "sleep")
	pid := cmd.Process.Pid
	require.True(t, ps.IsAlive(pid))

	start := time.Now()
	err := ps.Stop(ps.Entry{ID: strconv.Itoa(pid)}, 2*time.Second)
	require.NoError(t, err)

	// Should terminate well within the grace window because the helper
	// has no SIGTERM handler — default disposition is exit.
	elapsed := time.Since(start)
	assert.Less(t, elapsed, 2*time.Second, "expected graceful exit before grace expired")
	assert.True(t, waitGone(pid, time.Second), "process should be reaped after Stop")
}

func TestStop_Idempotent(t *testing.T) {
	cmd := startHelper(t, "sleep")
	pid := cmd.Process.Pid
	entry := ps.Entry{ID: strconv.Itoa(pid)}

	require.NoError(t, ps.Stop(entry, time.Second))
	require.True(t, waitGone(pid, time.Second))

	// Second call must be a no-op, not an error.
	require.NoError(t, ps.Stop(entry, time.Second))
}

func TestStop_AlreadyDead(t *testing.T) {
	// Simulate an entry that points at a never-existed pid. Stop must
	// recognize the corpse and return cleanly.
	entry := ps.Entry{ID: "2147483646"} // safely out of range
	require.NoError(t, ps.Stop(entry, 100*time.Millisecond))
}

func TestStop_RefusesSelf(t *testing.T) {
	entry := ps.Entry{ID: strconv.Itoa(os.Getpid())}
	err := ps.Stop(entry, 10*time.Millisecond)
	require.Error(t, err)
}

func TestStop_EmptyEntry(t *testing.T) {
	require.NoError(t, ps.Stop(ps.Entry{}, time.Second))
	require.NoError(t, ps.Stop(ps.Entry{ID: ""}, time.Second))
	require.NoError(t, ps.Stop(ps.Entry{ID: "garbage"}, time.Second))
}
