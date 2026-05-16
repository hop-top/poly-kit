package upgrade

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackupOrchestrator_BackupDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	orch := newBackupOrchestrator("myapp", "db", 3)
	dir, err := orch.backupDir("1.2.0")
	require.NoError(t, err)

	expected := filepath.Join(tmp, "hop", "myapp", "backups", "db", "pre-1.2.0")
	assert.Equal(t, expected, dir)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestBackupOrchestrator_Prune(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	base := filepath.Join(tmp, "hop", "myapp", "backups", "db")
	versions := []string{"pre-1.0.0", "pre-1.1.0", "pre-1.2.0", "pre-1.3.0", "pre-2.0.0"}
	for _, v := range versions {
		require.NoError(t, os.MkdirAll(filepath.Join(base, v), 0o750))
	}

	orch := newBackupOrchestrator("myapp", "db", 3)
	err := orch.prune()
	require.NoError(t, err)

	entries, err := os.ReadDir(base)
	require.NoError(t, err)

	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}

	assert.Equal(t, []string{"pre-1.2.0", "pre-1.3.0", "pre-2.0.0"}, names)
}

func TestBackupOrchestrator_PruneNoop(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	base := filepath.Join(tmp, "hop", "myapp", "backups", "db")
	require.NoError(t, os.MkdirAll(filepath.Join(base, "pre-1.0.0"), 0o750))

	orch := newBackupOrchestrator("myapp", "db", 5)
	err := orch.prune()
	require.NoError(t, err)

	entries, err := os.ReadDir(base)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestBackupOrchestrator_PruneNoDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	orch := newBackupOrchestrator("myapp", "db", 5)
	err := orch.prune()
	require.NoError(t, err) // no error when dir doesn't exist
}
