package fsdir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDriver_Name(t *testing.T) {
	d := New("data", "/tmp/data")
	assert.Equal(t, "data", d.Name())
}

func TestDriver_BackupRestore(t *testing.T) {
	tmp := t.TempDir()
	dirPath := filepath.Join(tmp, "mydata")
	require.NoError(t, os.MkdirAll(filepath.Join(dirPath, "sub"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dirPath, "file.txt"), []byte("hello"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dirPath, "sub", "nested.txt"), []byte("world"), 0o600))

	d := New("mydata", dirPath)

	backupDir := filepath.Join(tmp, "backup")
	err := d.Backup(backupDir)
	require.NoError(t, err)

	// Verify archive exists.
	archivePath := filepath.Join(backupDir, "mydata.tar.gz")
	_, err = os.Stat(archivePath)
	require.NoError(t, err)

	// Modify original.
	require.NoError(t, os.WriteFile(filepath.Join(dirPath, "file.txt"), []byte("changed"), 0o600))

	// Restore.
	err = d.Restore(backupDir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dirPath, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))

	nested, err := os.ReadFile(filepath.Join(dirPath, "sub", "nested.txt"))
	require.NoError(t, err)
	assert.Equal(t, "world", string(nested))
}

func TestDriver_BackupMissingDir(t *testing.T) {
	d := New("missing", "/nonexistent/dir")
	backupDir := t.TempDir()
	err := d.Backup(backupDir)
	require.NoError(t, err) // no error for missing dir
}

func TestDriver_RestoreMissingArchive(t *testing.T) {
	d := New("missing", "/tmp/whatever")
	err := d.Restore(t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
