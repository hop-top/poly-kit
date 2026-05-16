//go:build !windows

package ps_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/ps"
)

func TestStop_EscalatesToKill(t *testing.T) {
	// Helper installs a SIGTERM handler that ignores the signal, so
	// Stop must escalate to SIGKILL after grace expires.
	cmd := startHelper(t, "ignore-sigterm")
	pid := cmd.Process.Pid

	// Give the child a moment to install its handler before we signal.
	time.Sleep(100 * time.Millisecond)
	require.True(t, ps.IsAlive(pid))

	grace := 200 * time.Millisecond
	start := time.Now()
	err := ps.Stop(ps.Entry{ID: strconv.Itoa(pid)}, grace)
	require.NoError(t, err)

	elapsed := time.Since(start)
	assert.True(t, waitGone(pid, time.Second), "process should be dead after Stop")
	// Should have waited at least the grace window (SIGTERM ignored).
	// Allow some slack for the trailing SIGKILL + reap.
	assert.GreaterOrEqual(t, elapsed, grace,
		"expected at least grace window before SIGKILL")
	assert.Less(t, elapsed, grace+2*time.Second,
		"expected SIGKILL to land soon after grace expired")
}
