package ps_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/ps"
)

func TestWritePIDFile_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.pid")
	pid := os.Getpid()

	err := ps.WritePIDFile(path, ps.Entry{ID: strconv.Itoa(pid)})
	require.NoError(t, err)

	// Reader sees the PID we wrote.
	entry, err := ps.EntryFromPIDFile(path)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(pid), entry.ID)
	assert.Equal(t, ps.StatusRunning, entry.Status)
}

func TestWritePIDFile_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "sub", "service.pid")
	pid := os.Getpid()

	err := ps.WritePIDFile(path, ps.Entry{ID: strconv.Itoa(pid)})
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.False(t, info.IsDir())
}

func TestWritePIDFile_Mode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits behave differently on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "service.pid")
	pid := os.Getpid()

	err := ps.WritePIDFile(path, ps.Entry{ID: strconv.Itoa(pid)})
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	// Mask out non-permission bits.
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestWritePIDFile_Atomic_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.pid")

	require.NoError(t, ps.WritePIDFile(path, ps.Entry{ID: "12345"}))
	// A second write with a different pid replaces the file in place.
	require.NoError(t, ps.WritePIDFile(path, ps.Entry{ID: strconv.Itoa(os.Getpid())}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(os.Getpid())+"\n", string(data))
}

func TestWritePIDFile_RejectsBadID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.pid")

	cases := []struct {
		name string
		id   string
	}{
		{"empty", ""},
		{"non-numeric", "not-a-pid"},
		{"zero", "0"},
		{"negative", "-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ps.WritePIDFile(path, ps.Entry{ID: tc.id})
			require.Error(t, err)
			// File must not exist after a rejected write.
			_, statErr := os.Stat(path)
			assert.True(t, os.IsNotExist(statErr), "expected no file after rejected write")
		})
	}
}

func TestWritePIDFile_NoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.pid")
	require.NoError(t, ps.WritePIDFile(path, ps.Entry{ID: strconv.Itoa(os.Getpid())}))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	// Only the final pid file should remain — temp files cleaned up.
	require.Len(t, entries, 1)
	assert.Equal(t, "service.pid", entries[0].Name())
}
