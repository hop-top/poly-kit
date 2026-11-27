//go:build unix

package fsdir

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/upgrade"
)

// linkTree builds a directory with a regular file, a subdirectory, a
// one extra entry made by mk at "x".
func linkTree(t *testing.T, mk func(root string) error) (tmp, root string) {
	t.Helper()
	tmp = t.TempDir()
	root = filepath.Join(tmp, "mydata")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("hello"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "in.txt"), []byte("in"), 0o600))
	require.NoError(t, mk(root))
	return tmp, root
}

func TestDriver_BackupRefusesEntriesItCannotRestore(t *testing.T) {
	kinds := map[string]func(root string) error{
		"symlink to a file":      func(r string) error { return os.Symlink("file.txt", filepath.Join(r, "x")) },
		"symlink to a directory": func(r string) error { return os.Symlink("sub", filepath.Join(r, "x")) },
		"dangling symlink":       func(r string) error { return os.Symlink("missing", filepath.Join(r, "x")) },
		"symlink chain": func(r string) error {
			if err := os.Symlink("file.txt", filepath.Join(r, "z-base")); err != nil {
				return err
			}
			return os.Symlink("z-base", filepath.Join(r, "x"))
		},
		"absolute symlink": func(r string) error { return os.Symlink("/etc", filepath.Join(r, "x")) },
		"fifo":             func(r string) error { return syscall.Mkfifo(filepath.Join(r, "x"), 0o600) },
	}
	for name, mk := range kinds {
		t.Run(name, func(t *testing.T) {
			tmp, root := linkTree(t, mk)
			bk := filepath.Join(tmp, "bk")
			err := New("mydata", root).Backup(bk)
			require.Error(t, err)
			assert.Contains(t, err.Error(), filepath.Join(root, "x"), "error names the path")
			assert.Contains(t, err.Error(), "unsupported file type")
			ents, rerr := os.ReadDir(bk)
			require.NoError(t, rerr)
			assert.Empty(t, ents, "no archive, no partial file")
		})
	}
}

func TestDriver_BackupRefusesALinkAmongRegularEntries(t *testing.T) {
	// the link sorts before "sub": a half-written archive would lose sub/.
	tmp, root := linkTree(t, func(r string) error { return os.Symlink("file.txt", filepath.Join(r, "a-link")) })
	bk := filepath.Join(tmp, "bk")
	require.Error(t, New("mydata", root).Backup(bk))
	ents, err := os.ReadDir(bk)
	require.NoError(t, err)
	assert.Empty(t, ents)
}

func TestDriver_MigratorFailsTheSchemaBeforeAnyUpWhenBackupRefuses(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "store")
	require.NoError(t, os.MkdirAll(root, 0o750))
	require.NoError(t, os.Symlink("/etc", filepath.Join(root, "abs")))
	ran := false
	reg := upgrade.NewRegistry()
	reg.Register(upgrade.Migration{Version: "1.0.0", Schema: "store", Up: func(context.Context) error {
		ran = true
		return nil
	}})
	m := upgrade.NewMigrator("demo", "1.0.0", upgrade.WithRegistry(reg), upgrade.WithDataDir(filepath.Join(tmp, "data")))
	m.AddDriver(NewWithOptions("store", root, WithDataDir(filepath.Join(tmp, "data"))))
	err := m.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migrate store: backup: ")
	assert.Contains(t, err.Error(), filepath.Join(root, "abs"))
	assert.False(t, ran, "no Up ran")
}
