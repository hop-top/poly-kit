package xdg_test

// Gap tests for `hop.top/kit/go/core/xdg`. Surfaced by tlc, rsx, and
// ctxt all reimplementing the same `<dbDir>/.dbs/<ts>.<name>.bak`
// dance.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/xdg"
)

// Gap: kit/core/xdg has no BackupBeforeMigrate helper.
//
// sqlstore.BackupBeforeMigrate (go/storage/sqlstore/backup.go:24)
// already does this for SQLite databases — including a WAL
// checkpoint. But it is sqlite-specific and lives in storage/sqlstore.
// tlc, rsx, and ctxt each hand-rolled the same pattern (per-tool
// backup dir, timestamped name, mkdir, copy) for non-DB files: state
// dirs, JSON snapshots, metadata blobs. Per the auto-memory note
// "DB backups in .tlc/.dbs/", the convention is shared but the
// helper is not.
//
// Desired API (in xdg, since the location convention is XDG-driven):
//
//	backup, err := xdg.BackupBeforeMigrate(srcPath, migrator)
//	// backup placed at <dirOf(srcPath)>/.dbs/<ts>.<basename>
//	// migrator is called only after backup succeeds; on migrator
//	// error, original is restored from backup.
func TestGap_XDGBackupBeforeMigrate_Missing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "state.json")
	original := []byte(`{"v":1}`)
	require.NoError(t, os.WriteFile(src, original, 0o600))

	migrated := []byte(`{"v":2}`)
	backupPath, err := xdg.BackupBeforeMigrate(src, func(p string) error {
		return os.WriteFile(p, migrated, 0o600)
	})
	require.NoError(t, err)
	require.NotEmpty(t, backupPath)

	// Backup lives under <dir>/.dbs/.
	require.Equal(t, filepath.Join(dir, ".dbs"), filepath.Dir(backupPath))

	// Backup carries the original content.
	gotBackup, err := os.ReadFile(backupPath)
	require.NoError(t, err)
	require.Equal(t, original, gotBackup)

	// Live file carries the migrated content.
	gotLive, err := os.ReadFile(src)
	require.NoError(t, err)
	require.Equal(t, migrated, gotLive)
}
