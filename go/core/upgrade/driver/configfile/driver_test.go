package configfile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDriver_Name(t *testing.T) {
	d := New("/tmp/config.yaml")
	assert.Equal(t, "config", d.Name())
}

func TestDriver_BackupRestore(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("key: value"), 0o600))

	d := New(cfgPath)

	backupDir := filepath.Join(tmp, "backup")
	err := d.Backup(backupDir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(backupDir, backupFileName(cfgPath)))
	require.NoError(t, err)
	assert.Equal(t, "key: value", string(data))

	// Modify original.
	require.NoError(t, os.WriteFile(cfgPath, []byte("key: changed"), 0o600))

	// Restore.
	err = d.Restore(backupDir)
	require.NoError(t, err)

	restored, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "key: value", string(restored))
}

func TestDriver_BackupMultipleFiles(t *testing.T) {
	tmp := t.TempDir()
	f1 := filepath.Join(tmp, "a.yaml")
	f2 := filepath.Join(tmp, "b.json")
	require.NoError(t, os.WriteFile(f1, []byte("a"), 0o600))
	require.NoError(t, os.WriteFile(f2, []byte("b"), 0o600))

	d := New(f1, f2)
	backupDir := filepath.Join(tmp, "backup")
	err := d.Backup(backupDir)
	require.NoError(t, err)

	data1, err := os.ReadFile(filepath.Join(backupDir, backupFileName(f1)))
	require.NoError(t, err)
	assert.Equal(t, "a", string(data1))

	data2, err := os.ReadFile(filepath.Join(backupDir, backupFileName(f2)))
	require.NoError(t, err)
	assert.Equal(t, "b", string(data2))
}

func TestDriver_BackupMissingFile(t *testing.T) {
	d := New("/nonexistent/file.yaml")
	backupDir := t.TempDir()
	err := d.Backup(backupDir)
	require.NoError(t, err) // missing files are skipped
}
