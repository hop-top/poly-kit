package sqlite

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/upgrade"
)

// Committed rows still in the WAL are in the backup. A raw copy of the
// main file misses them.
func TestBackup_ContainsRowsStillInWAL(t *testing.T) {
	for name, withDB := range map[string]bool{"path only": false, "with db": true} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			dbPath := join(dir, "app.db")
			db := openWAL(t, dbPath)
			insert(t, db, 1, 2, 3)
			st, err := os.Stat(dbPath + "-wal")
			require.NoError(t, err, "rows must sit in the WAL")
			require.NotZero(t, st.Size())

			var opts []Option
			if withDB {
				opts = append(opts, WithDB(db))
			}
			bk := join(dir, "backup")
			require.NoError(t, New(dbPath, opts...).Backup(bk))

			assert.Equal(t, []int{1, 2, 3}, rows(t, join(bk, "app.db")))
			requireIntegrity(t, join(bk, "app.db"))
			assert.False(t, exists(join(bk, "app.db-wal")), "backup is one file")
			assert.False(t, exists(join(bk, "app.db.tmp")), "no temp file left")
		})
	}
}

// Paths are interpolated into SQL by VACUUM INTO and into a URI by the
// driver's own connection: odd directory names must stay data.
func TestBackup_OddPaths(t *testing.T) {
	for _, odd := range []string{"with space", "o'brien", `q"uote`, "a?b#c%d", "x;DROP TABLE t;--"} {
		t.Run(odd, func(t *testing.T) {
			dir := join(t.TempDir(), odd)
			require.NoError(t, os.MkdirAll(dir, 0o750))
			dbPath := join(dir, "my db'.sqlite")
			db := openWAL(t, dbPath)
			insert(t, db, 7)

			bk := join(dir, "back up", odd)
			require.NoError(t, New(dbPath).Backup(bk))
			assert.Equal(t, []int{7}, rows(t, join(bk, "my db'.sqlite")))

			require.NoError(t, New(dbPath, WithDB(db)).Backup(bk), "second backup replaces the first")
			assert.Equal(t, []int{7}, rows(t, join(bk, "my db'.sqlite")))
			assert.Equal(t, []int{7}, rows(t, dbPath), "source untouched")
		})
	}
}

func TestBackup_ReplacesStaleBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := join(dir, "app.db")
	db := openWAL(t, dbPath)
	insert(t, db, 1)
	d := New(dbPath)
	bk := join(dir, "bk")
	require.NoError(t, d.Backup(bk))
	insert(t, db, 2)
	require.NoError(t, d.Backup(bk))
	assert.Equal(t, []int{1, 2}, rows(t, join(bk, "app.db")))
}

func TestBackup_NotADatabaseFails(t *testing.T) {
	dir := t.TempDir()
	dbPath := join(dir, "app.db")
	require.NoError(t, os.WriteFile(dbPath, []byte("not sqlite, just bytes"), 0o600))
	err := New(dbPath).Backup(join(dir, "bk"))
	require.Error(t, err)
	assert.NotErrorIs(t, err, upgrade.ErrBackupUnsupported)
	assert.False(t, exists(join(dir, "bk", "app.db")), "no partial backup")
}

// The caller's handle is borrowed: still open and usable after Backup.
func TestBackup_WithDBDoesNotCloseHandle(t *testing.T) {
	dir := t.TempDir()
	dbPath := join(dir, "app.db")
	db := openWAL(t, dbPath)
	insert(t, db, 1)
	d := New(dbPath, WithDB(db))
	require.NoError(t, d.Backup(join(dir, "bk")))
	require.NoError(t, db.Ping(), "handle closed by Backup")
	insert(t, db, 2)
	assert.Equal(t, []int{1, 2}, rows(t, dbPath))
	// Failure and unsupported Restore leave it open too.
	require.Error(t, d.Backup(filepath.Join(dbPath, "nope")))
	require.ErrorIs(t, d.Restore(join(dir, "bk")), upgrade.ErrBackupUnsupported)
	require.NoError(t, db.Ping())
}

// With a handle and no path the backup is named after the schema, and
// an in-memory database can be backed up.
func TestBackup_WithDBNoPath(t *testing.T) {
	db := openWAL(t, join(t.TempDir(), "app.db"))
	insert(t, db, 5)
	bk := t.TempDir()
	require.NoError(t, New("", WithName("appdb"), WithDB(db)).Backup(bk))
	assert.Equal(t, []int{5}, rows(t, join(bk, "appdb.db")))
}

// Restore after a later write returns the earlier state. A crashed
// writer's -wal, -shm and -journal sit beside the file: after Restore
// none is left, so SQLite replays nothing onto the restored database.
func TestRestore_PathOnlyReturnsEarlierStateWithoutSidecars(t *testing.T) {
	dir := t.TempDir()
	dbPath := join(dir, "app.db")
	db := openWAL(t, dbPath)
	insert(t, db, 1, 2)
	d := New(dbPath)
	bk := join(dir, "bk")
	require.NoError(t, d.Backup(bk))

	insert(t, db, 3, 4) // later write, still only in the WAL
	// Freeze the crashed state: main file plus WAL holding rows 3 and 4.
	crash := join(dir, "crash")
	require.NoError(t, os.MkdirAll(crash, 0o750))
	copyFile(t, dbPath, join(crash, "app.db"))
	copyFile(t, dbPath+"-wal", join(crash, "app.db-wal"))
	require.NoError(t, db.Close()) // precondition for Restore: no handle
	require.NoError(t, os.Remove(dbPath))
	copyFile(t, join(crash, "app.db"), dbPath)
	copyFile(t, join(crash, "app.db-wal"), dbPath+"-wal")
	require.NoError(t, os.WriteFile(dbPath+"-shm", []byte("stale"), 0o600))
	require.NoError(t, os.WriteFile(dbPath+"-journal", []byte("stale"), 0o600))

	require.NoError(t, d.Restore(bk))

	for _, p := range sidecarPaths(dbPath) {
		assert.False(t, exists(p), "stale sidecar %s", filepath.Base(p))
	}
	assert.Equal(t, []int{1, 2}, rows(t, dbPath))
	requireIntegrity(t, dbPath)
	assert.Equal(t, []int{1, 2}, rows(t, join(bk, "app.db")), "backup kept")
	ents, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range ents {
		assert.NotContains(t, e.Name(), ".restore-", "temp file left")
	}
}

func TestRestore_KeepsPermissions(t *testing.T) {
	dir := t.TempDir()
	dbPath := join(dir, "app.db")
	db := openWAL(t, dbPath)
	insert(t, db, 1)
	bk := join(dir, "bk")
	require.NoError(t, New(dbPath).Backup(bk))
	require.NoError(t, db.Close())
	require.NoError(t, os.Chmod(dbPath, 0o640))
	require.NoError(t, New(dbPath).Restore(bk))
	st, err := os.Stat(dbPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), st.Mode().Perm())
}

func TestRestore_MissingBackupFails(t *testing.T) {
	dir := t.TempDir()
	dbPath := join(dir, "app.db")
	db := openWAL(t, dbPath)
	insert(t, db, 1)
	require.NoError(t, db.Close())
	require.Error(t, New(dbPath).Restore(join(dir, "none")))
	assert.Equal(t, []int{1}, rows(t, dbPath), "database untouched")
}

// With a handle Restore does nothing and says so; the file and the
// backup stay as they were.
func TestRestore_WithDBUnsupported(t *testing.T) {
	dir := t.TempDir()
	dbPath := join(dir, "app.db")
	db := openWAL(t, dbPath)
	insert(t, db, 1)
	d := New(dbPath, WithDB(db))
	bk := join(dir, "bk")
	require.NoError(t, d.Backup(bk))
	insert(t, db, 2)

	err := d.Restore(bk)
	require.ErrorIs(t, err, upgrade.ErrBackupUnsupported)
	assert.Equal(t, []int{1, 2}, rows(t, dbPath), "database untouched")
	assert.Equal(t, []int{1}, rows(t, join(bk, "app.db")), "backup kept")
}
