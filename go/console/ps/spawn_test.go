package ps_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/ps"
)

func TestSpawnDetached_WritesPIDFile(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "svc.pid")

	cmd := newHelperCmd(t, "sleep")
	s, err := ps.SpawnDetached(context.Background(), cmd, ps.SpawnOptions{PIDFile: pidPath})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = ps.Stop(ps.Entry{ID: strconv.Itoa(s.PID)}, time.Second)
	})

	require.Greater(t, s.PID, 0)

	// PID file exists, has expected content + mode.
	data, err := os.ReadFile(pidPath)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(s.PID)+"\n", string(data))

	if runtime.GOOS != "windows" {
		info, err := os.Stat(pidPath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	// EntryFromPIDFile sees the child as alive.
	entry, err := ps.EntryFromPIDFile(pidPath)
	require.NoError(t, err)
	assert.Equal(t, ps.StatusRunning, entry.Status)
}

func TestSpawnDetached_RejectsNilCmd(t *testing.T) {
	_, err := ps.SpawnDetached(context.Background(), nil, ps.SpawnOptions{PIDFile: "x"})
	require.Error(t, err)
}

func TestSpawnDetached_RequiresPIDFile(t *testing.T) {
	cmd := newHelperCmd(t, "sleep")
	_, err := ps.SpawnDetached(context.Background(), cmd, ps.SpawnOptions{})
	require.Error(t, err)
}

func TestSpawnDetached_CanceledContext(t *testing.T) {
	dir := t.TempDir()
	cmd := newHelperCmd(t, "sleep")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ps.SpawnDetached(ctx, cmd, ps.SpawnOptions{
		PIDFile: filepath.Join(dir, "svc.pid"),
	})
	require.Error(t, err)
}

// E2E: full spawn → assert pid file → graceful Stop → idempotent Stop.
func TestSpawnDetached_E2E_GracefulStop(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "e2e.pid")

	cmd := newHelperCmd(t, "sleep")
	s, err := ps.SpawnDetached(context.Background(), cmd, ps.SpawnOptions{PIDFile: pidPath})
	require.NoError(t, err)

	// Initial state: alive.
	require.True(t, ps.IsAlive(s.PID))

	// Stop with reasonable grace.
	require.NoError(t, ps.Stop(ps.Entry{ID: strconv.Itoa(s.PID)}, time.Second))
	require.True(t, waitGone(s.PID, 2*time.Second))

	// Idempotence: second Stop on dead process is a no-op.
	require.NoError(t, ps.Stop(ps.Entry{ID: strconv.Itoa(s.PID)}, 100*time.Millisecond))

	// Wait on the handle returns once the reaper sees the exit. We
	// don't assert the error because graceful exit codes vary by
	// platform and shell wrapping.
	_ = s.Wait()
}

func TestSpawnDetached_StdioBuffer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stdio buffer test uses POSIX echo")
	}
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "echo.pid")

	cmd := newEchoCmd(t, "hello-from-spawn")
	s, err := ps.SpawnDetached(context.Background(), cmd, ps.SpawnOptions{
		PIDFile: pidPath,
		Stdout:  ps.StdioBuffer,
	})
	require.NoError(t, err)
	require.NoError(t, s.Wait())

	require.NotNil(t, s.Stdout())
	assert.True(t, strings.Contains(s.Stdout().String(), "hello-from-spawn"),
		"expected echo output captured: %q", s.Stdout().String())
}

func TestSpawnDetached_StdioDiscard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX echo")
	}
	dir := t.TempDir()
	cmd := newEchoCmd(t, "discard-me")
	s, err := ps.SpawnDetached(context.Background(), cmd, ps.SpawnOptions{
		PIDFile: filepath.Join(dir, "d.pid"),
		Stdout:  ps.StdioDiscard,
	})
	require.NoError(t, err)
	require.NoError(t, s.Wait())
	assert.Nil(t, s.Stdout(), "Discard mode should not allocate buffer")
}

func TestSpawnDetached_StdioFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX echo")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "child.log")
	cmd := newEchoCmd(t, "to-file")
	s, err := ps.SpawnDetached(context.Background(), cmd, ps.SpawnOptions{
		PIDFile:    filepath.Join(dir, "f.pid"),
		Stdout:     ps.StdioFile,
		StdoutPath: logPath,
	})
	require.NoError(t, err)
	require.NoError(t, s.Wait())

	// Give the reaper a beat to flush+close the file.
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(logPath)
		return err == nil && strings.Contains(string(data), "to-file")
	}, time.Second, 20*time.Millisecond)
}

func TestSpawnDetached_StdioFile_RequiresPath(t *testing.T) {
	cmd := newEchoCmd(t, "x")
	_, err := ps.SpawnDetached(context.Background(), cmd, ps.SpawnOptions{
		PIDFile: filepath.Join(t.TempDir(), "p.pid"),
		Stdout:  ps.StdioFile,
	})
	require.Error(t, err)
}
