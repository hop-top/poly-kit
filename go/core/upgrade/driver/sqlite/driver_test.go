package sqlite

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDriver_Name(t *testing.T) {
	d := New("/tmp/test.db")
	assert.Equal(t, "sqlite", d.Name())

	d2 := New("/tmp/test.db", WithName("mydb"))
	assert.Equal(t, "mydb", d2.Name())
}

func TestDriver_BackupRestore(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	require.NoError(t, os.WriteFile(dbPath, []byte("sqlite data"), 0o600))

	d := New(dbPath)

	backupDir := filepath.Join(tmp, "backup")
	err := d.Backup(backupDir)
	require.NoError(t, err)

	// Verify backup exists.
	backupFile := filepath.Join(backupDir, "test.db")
	data, err := os.ReadFile(backupFile)
	require.NoError(t, err)
	assert.Equal(t, "sqlite data", string(data))

	// Modify original.
	require.NoError(t, os.WriteFile(dbPath, []byte("modified"), 0o600))

	// Restore.
	err = d.Restore(backupDir)
	require.NoError(t, err)

	restored, err := os.ReadFile(dbPath)
	require.NoError(t, err)
	assert.Equal(t, "sqlite data", string(restored))
}

func TestDriver_BackupMissingDB(t *testing.T) {
	d := New("/nonexistent/path/test.db")

	backupDir := t.TempDir()
	err := d.Backup(backupDir)
	require.NoError(t, err) // no error for missing db
}

func TestDriver_Version(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	d := New("/tmp/test.db", WithTool("testapp"))
	v, err := d.Version()
	require.NoError(t, err)
	assert.Equal(t, "", v)
}
