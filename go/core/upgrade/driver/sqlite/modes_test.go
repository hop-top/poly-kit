package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/upgrade"
)

// runEnv is a database at 1.0.0 (row 1, in the WAL) with an isolated
// data dir, so nothing touches the user's XDG dirs.
type runEnv struct {
	dir, dbPath, dataDir string
	db                   *sql.DB
}

func newRunEnv(t *testing.T) runEnv {
	t.Helper()
	e := runEnv{dir: t.TempDir()}
	e.dbPath = join(e.dir, "app.db")
	e.dataDir = join(e.dir, "data")
	e.db = openWAL(t, e.dbPath)
	insert(t, e.db, 1)
	require.NoError(t, os.MkdirAll(join(e.dataDir, "appdb"), 0o750))
	require.NoError(t, os.WriteFile(join(e.dataDir, "appdb", "version"), []byte("1.0.0"), 0o600))
	return e
}

func (e runEnv) version(t *testing.T) string {
	t.Helper()
	v, err := upgrade.ReadVersionFileIn(e.dataDir, "appdb")
	require.NoError(t, err)
	return v
}

func (e runEnv) driver(opts ...Option) *Driver {
	return New(e.dbPath, append([]Option{WithName("appdb"), WithDataDir(e.dataDir)}, opts...)...)
}

// registry: 1.1.0 inserts row 2; 1.2.0 closes release (when set, the
// caller's handle going away before a path-only Restore) and fails.
func (e runEnv) registry(withDown bool, before1_2 func()) *upgrade.Registry {
	reg := upgrade.NewRegistry()
	reg.Register(upgrade.Migration{Version: "1.1.0", Schema: "appdb",
		Up: func(context.Context) error { _, err := e.db.Exec(`INSERT INTO t (x) VALUES (2)`); return err },
		Down: func() func(context.Context) error {
			if !withDown {
				return nil
			}
			return func(context.Context) error { _, err := e.db.Exec(`DELETE FROM t WHERE x = 2`); return err }
		}(),
	})
	reg.Register(upgrade.Migration{Version: "1.2.0", Schema: "appdb",
		Up: func(context.Context) error {
			if before1_2 != nil {
				before1_2()
			}
			return errors.New("simulated failure")
		},
	})
	return reg
}

func (e runEnv) migrator(reg *upgrade.Registry, d *Driver, opts ...upgrade.MigratorOption) *upgrade.Migrator {
	m := upgrade.NewMigrator("tool", "1.2.0", append([]upgrade.MigratorOption{
		upgrade.WithRegistry(reg), upgrade.WithDataDir(e.dataDir)}, opts...)...)
	m.AddDriver(d)
	return m
}

func requireLockFree(t *testing.T, d *Driver) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	unlock, err := d.Lock(ctx)
	require.NoError(t, err, "lock still held after the run")
	require.NoError(t, unlock())
}

// Path only: a failed run restores the file; the lock sits beside the
// version file and is free afterwards.
func TestRun_PathOnlyFailureRestoresFile(t *testing.T) {
	e := newRunEnv(t)
	d := e.driver()
	reg := e.registry(false, func() { require.NoError(t, e.db.Close()) })
	err := e.migrator(reg, d).Run(context.Background())
	require.ErrorContains(t, err, "simulated failure")
	require.NotErrorIs(t, err, upgrade.ErrNotRestored)

	assert.Equal(t, []int{1}, rows(t, e.dbPath), "restored to the pre-run state")
	assert.Equal(t, "1.0.0", e.version(t))
	assert.True(t, exists(join(e.dataDir, "appdb", ".lock")), "lock beside the version file")
	requireLockFree(t, d)
}

// With a handle there is no restore: a Down saves the run, a missing
// Down ends it not restored, and the backup stays for manual recovery.
func TestRun_WithDBMissingDownIsNotRestored(t *testing.T) {
	e := newRunEnv(t)
	d := e.driver(WithDB(e.db))
	err := e.migrator(e.registry(false, nil), d).Run(context.Background())
	require.ErrorIs(t, err, upgrade.ErrNotRestored)
	require.ErrorIs(t, err, upgrade.ErrBackupUnsupported)
	require.ErrorContains(t, err, "simulated failure")

	assert.Equal(t, "1.1.0", e.version(t), "stays at the last step applied")
	assert.Equal(t, []int{1, 2}, rows(t, e.dbPath))
	assert.Equal(t, []int{1}, rows(t, join(e.dataDir, "backups", "appdb", "pre-1.1.0", "app.db")))
	require.NoError(t, e.db.Ping(), "caller's handle closed")
	requireLockFree(t, d)
}

func TestRun_WithDBDownRollsBack(t *testing.T) {
	e := newRunEnv(t)
	d := e.driver(WithDB(e.db))
	err := e.migrator(e.registry(true, nil), d).Run(context.Background())
	require.ErrorContains(t, err, "simulated failure")
	require.NotErrorIs(t, err, upgrade.ErrNotRestored)
	assert.Equal(t, []int{1}, rows(t, e.dbPath))
	assert.Equal(t, "1.0.0", e.version(t))
	require.NoError(t, e.db.Ping())
}

// Manual rollback, path only: RollbackLatest restores the backup and
// records the version the run started at.
func TestRollbackLatest_PathOnly(t *testing.T) {
	e := newRunEnv(t)
	d := e.driver()
	m := e.migrator(e.registry(false, nil), d, upgrade.WithManualRollback())
	require.ErrorContains(t, m.Run(context.Background()), "simulated failure")
	require.Equal(t, "1.1.0", e.version(t))
	require.Equal(t, []int{1, 2}, rows(t, e.dbPath))

	require.NoError(t, e.db.Close()) // precondition: no handle on the file
	require.NoError(t, m.RollbackLatest())
	assert.Equal(t, []int{1}, rows(t, e.dbPath))
	assert.Equal(t, "1.0.0", e.version(t))
	for _, p := range sidecarPaths(e.dbPath) {
		assert.False(t, exists(p), p)
	}
	requireLockFree(t, d)
}

// Manual rollback with a handle: nothing to restore with, reported as
// not restored; the version stays and the handle stays open.
func TestRollbackLatest_WithDB(t *testing.T) {
	e := newRunEnv(t)
	d := e.driver(WithDB(e.db))
	m := e.migrator(e.registry(false, nil), d, upgrade.WithManualRollback())
	require.ErrorContains(t, m.Run(context.Background()), "simulated failure")

	err := m.RollbackLatest()
	require.ErrorIs(t, err, upgrade.ErrNotRestored)
	require.ErrorIs(t, err, upgrade.ErrBackupUnsupported)
	assert.Equal(t, "1.1.0", e.version(t))
	assert.Equal(t, []int{1, 2}, rows(t, e.dbPath))
	require.NoError(t, e.db.Ping())
	requireLockFree(t, d)
}

// A successful run in handle mode leaves a backup with the rows that
// were still in the WAL.
func TestRun_WithDBBackupHasWALRows(t *testing.T) {
	e := newRunEnv(t)
	reg := upgrade.NewRegistry()
	reg.Register(upgrade.Migration{Version: "1.1.0", Schema: "appdb",
		Up: func(context.Context) error { _, err := e.db.Exec(`INSERT INTO t (x) VALUES (2)`); return err }})
	m := upgrade.NewMigrator("tool", "1.1.0", upgrade.WithRegistry(reg), upgrade.WithDataDir(e.dataDir))
	m.AddDriver(e.driver(WithDB(e.db)))
	require.NoError(t, m.Run(context.Background()))
	assert.Equal(t, []int{1}, rows(t, join(e.dataDir, "backups", "appdb", "pre-1.1.0", "app.db")))
	assert.Equal(t, []int{1, 2}, rows(t, e.dbPath))
	assert.Equal(t, "1.1.0", e.version(t))
}
