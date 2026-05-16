package upgrade

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDriver implements SchemaDriver for testing.
type mockDriver struct {
	name       string
	version    string
	backupErr  error
	restoreErr error
	backed     bool
	restored   bool
	backupDest string
}

func (d *mockDriver) Name() string              { return d.name }
func (d *mockDriver) Version() (string, error)  { return d.version, nil }
func (d *mockDriver) SetVersion(v string) error { d.version = v; return nil }
func (d *mockDriver) Backup(dest string) error {
	d.backed = true
	d.backupDest = dest
	return d.backupErr
}
func (d *mockDriver) Restore(src string) error {
	d.restored = true
	return d.restoreErr
}

func TestMigrator_Run_HappyPath(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	var applied []string
	RegisterMigration(Migration{
		Version: "1.1.0",
		Schema:  "testdb",
		Up:      func(ctx context.Context) error { applied = append(applied, "1.1.0"); return nil },
	})
	RegisterMigration(Migration{
		Version: "1.2.0",
		Schema:  "testdb",
		Up:      func(ctx context.Context) error { applied = append(applied, "1.2.0"); return nil },
	})

	t.Setenv("XDG_DATA_HOME", t.TempDir())

	d := &mockDriver{name: "testdb", version: "1.0.0"}
	m := NewMigrator("testapp", "1.2.0")
	m.AddDriver(d)

	err := m.Run(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []string{"1.1.0", "1.2.0"}, applied)
	assert.Equal(t, "1.2.0", d.version)
	assert.True(t, d.backed)
}

func TestMigrator_Run_NoPending(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	t.Setenv("XDG_DATA_HOME", t.TempDir())

	d := &mockDriver{name: "testdb", version: "2.0.0"}
	m := NewMigrator("testapp", "2.0.0")
	m.AddDriver(d)

	err := m.Run(context.Background())
	require.NoError(t, err)
	assert.False(t, d.backed)
}

func TestMigrator_Run_RollbackOnFailure(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	var applied []string
	RegisterMigration(Migration{
		Version: "1.1.0",
		Schema:  "testdb",
		Up:      func(ctx context.Context) error { applied = append(applied, "1.1.0"); return nil },
		Down:    func(ctx context.Context) error { return nil },
	})
	RegisterMigration(Migration{
		Version: "1.2.0",
		Schema:  "testdb",
		Up:      func(ctx context.Context) error { return errors.New("boom") },
	})

	t.Setenv("XDG_DATA_HOME", t.TempDir())

	d := &mockDriver{name: "testdb", version: "1.0.0"}
	m := NewMigrator("testapp", "1.2.0")
	m.AddDriver(d)

	err := m.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
	// Down was called for 1.1.0 (the only applied migration).
}

func TestMigrator_Run_RestoreFallback(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	RegisterMigration(Migration{
		Version: "1.1.0",
		Schema:  "testdb",
		Up:      func(ctx context.Context) error { return nil },
		Down:    nil, // no Down => Restore path
	})
	RegisterMigration(Migration{
		Version: "1.2.0",
		Schema:  "testdb",
		Up:      func(ctx context.Context) error { return errors.New("fail") },
	})

	t.Setenv("XDG_DATA_HOME", t.TempDir())

	d := &mockDriver{name: "testdb", version: "1.0.0"}
	m := NewMigrator("testapp", "1.2.0")
	m.AddDriver(d)

	err := m.Run(context.Background())
	require.Error(t, err)
	assert.True(t, d.restored)
}

func TestMigrator_ManualRollback(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	RegisterMigration(Migration{
		Version: "1.1.0",
		Schema:  "testdb",
		Up:      func(ctx context.Context) error { return errors.New("fail") },
	})

	t.Setenv("XDG_DATA_HOME", t.TempDir())

	d := &mockDriver{name: "testdb", version: "1.0.0"}
	m := NewMigrator("testapp", "1.1.0", WithManualRollback())
	m.AddDriver(d)

	err := m.Run(context.Background())
	require.Error(t, err)
	assert.False(t, d.restored) // no auto-rollback
}

func TestReadVersionFile_NotExist(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	v, err := ReadVersionFile("testapp", "testdb")
	require.NoError(t, err)
	assert.Equal(t, "", v)
}

func TestReadVersionFile_Exists(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	dir := filepath.Join(tmp, "hop", "testapp", "testdb")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "version"), []byte("1.5.0\n"), 0o600))

	v, err := ReadVersionFile("testapp", "testdb")
	require.NoError(t, err)
	assert.Equal(t, "1.5.0", v)
}
