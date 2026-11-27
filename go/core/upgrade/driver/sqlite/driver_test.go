package sqlite

import (
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
