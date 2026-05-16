package upgrade

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// sqliteTestDriver is a SchemaDriver backed by a real SQLite DB file,
// with version tracking via the xdg-based version file (same as production).
type sqliteTestDriver struct {
	name   string
	tool   string
	dbPath string
	db     *sql.DB
}

func newSQLiteTestDriver(t *testing.T, name, tool, dbPath string) *sqliteTestDriver {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return &sqliteTestDriver{name: name, tool: tool, dbPath: dbPath, db: db}
}

func (d *sqliteTestDriver) Name() string { return d.name }

func (d *sqliteTestDriver) Version() (string, error) {
	return ReadVersionFile(d.tool, d.name)
}

func (d *sqliteTestDriver) SetVersion(_ string) error {
	return nil // version file is written by Migrator.setVersion
}

func (d *sqliteTestDriver) Backup(dest string) error {
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return err
	}
	src, err := os.ReadFile(d.dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.WriteFile(filepath.Join(dest, filepath.Base(d.dbPath)), src, 0o600)
}

func (d *sqliteTestDriver) Restore(src string) error {
	data, err := os.ReadFile(filepath.Join(src, filepath.Base(d.dbPath)))
	if err != nil {
		return err
	}
	// Close and reopen after restore.
	d.db.Close()
	if err := os.WriteFile(d.dbPath, data, 0o600); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", d.dbPath)
	if err != nil {
		return err
	}
	d.db = db
	return nil
}

func TestE2E_FullMigrationLifecycle(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	dbPath := filepath.Join(tmp, "app.db")
	d := newSQLiteTestDriver(t, "e2edb", "e2etool", dbPath)

	// Create initial schema.
	_, err := d.db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)`)
	require.NoError(t, err)
	_, err = d.db.Exec(`INSERT INTO items (name) VALUES ('initial')`)
	require.NoError(t, err)

	// Set initial version file.
	vDir := filepath.Join(tmp, "hop", "e2etool", "e2edb")
	require.NoError(t, os.MkdirAll(vDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(vDir, "version"), []byte("1.0.0"), 0o600))

	// Register migrations for v1.0.0 → v1.1.0 → v1.2.0.
	RegisterMigration(Migration{
		Version: "1.1.0", Schema: "e2edb",
		Up: func(ctx context.Context) error {
			_, err := d.db.Exec(`ALTER TABLE items ADD COLUMN desc TEXT DEFAULT ''`)
			return err
		},
		Down: func(ctx context.Context) error {
			return nil
		},
	})
	RegisterMigration(Migration{
		Version: "1.2.0", Schema: "e2edb",
		Up: func(ctx context.Context) error {
			_, err := d.db.Exec(`CREATE TABLE tags (id INTEGER PRIMARY KEY, item_id INTEGER, tag TEXT)`)
			return err
		},
		Down: func(ctx context.Context) error {
			_, err := d.db.Exec(`DROP TABLE IF EXISTS tags`)
			return err
		},
	})

	// Step 1: Run from v1.0.0 → v1.2.0.
	m := NewMigrator("e2etool", "1.2.0")
	m.AddDriver(d)
	require.NoError(t, m.Run(context.Background()))

	// Verify all applied in order.
	hist := m.History()
	require.Len(t, hist, 2)
	assert.Equal(t, "1.1.0", hist[0].Version)
	assert.Equal(t, "1.2.0", hist[1].Version)

	// Verify schema changes applied.
	var cnt int
	err = d.db.QueryRow(`SELECT count(*) FROM pragma_table_info('items') WHERE name = 'desc'`).Scan(&cnt)
	require.NoError(t, err)
	assert.Equal(t, 1, cnt, "desc column should exist")

	err = d.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'tags'`).Scan(&cnt)
	require.NoError(t, err)
	assert.Equal(t, 1, cnt, "tags table should exist")

	// Verify version file updated.
	v, err := ReadVersionFile("e2etool", "e2edb")
	require.NoError(t, err)
	assert.Equal(t, "1.2.0", v)

	// Step 2: Run again → no-op.
	m2 := NewMigrator("e2etool", "1.2.0")
	m2.AddDriver(d)
	require.NoError(t, m2.Run(context.Background()))
	assert.Empty(t, m2.History(), "should be no-op when already current")

	// Step 3: Register v1.3.0 migration that fails → verify rollback.
	RegisterMigration(Migration{
		Version: "1.3.0", Schema: "e2edb",
		Up: func(ctx context.Context) error {
			return errors.New("v1.3.0 migration failed")
		},
	})

	m3 := NewMigrator("e2etool", "1.3.0")
	m3.AddDriver(d)
	err = m3.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "v1.3.0 migration failed")

	// Version should still be 1.2.0.
	v, err = ReadVersionFile("e2etool", "e2edb")
	require.NoError(t, err)
	assert.Equal(t, "1.2.0", v)
}

func TestE2E_FirstTimeUser(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	dbPath := filepath.Join(tmp, "fresh.db")
	d := newSQLiteTestDriver(t, "freshdb", "freshtool", dbPath)

	_, err := d.db.Exec(`CREATE TABLE data (id INTEGER PRIMARY KEY)`)
	require.NoError(t, err)

	// No version file exists — first-time user.
	m := NewMigrator("freshtool", "1.0.0")
	m.AddDriver(d)
	require.NoError(t, m.Run(context.Background()))

	// No migrations should have run (no pending between "" and "1.0.0"
	// unless registered). But all registered with version <= 1.0.0 would
	// run. Since we registered none, history should be empty.
	assert.Empty(t, m.History())
}

func TestE2E_MultiStepFailureRestoresBackup(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	dbPath := filepath.Join(tmp, "multi.db")
	d := newSQLiteTestDriver(t, "multidb", "multitool", dbPath)

	_, err := d.db.Exec(`CREATE TABLE base (id INTEGER PRIMARY KEY)`)
	require.NoError(t, err)

	vDir := filepath.Join(tmp, "hop", "multitool", "multidb")
	require.NoError(t, os.MkdirAll(vDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(vDir, "version"), []byte("1.0.0"), 0o600))

	// v1.1.0 succeeds, v1.2.0 fails. No Down → Restore path.
	RegisterMigration(Migration{
		Version: "1.1.0", Schema: "multidb",
		Up: func(ctx context.Context) error {
			_, err := d.db.Exec(`ALTER TABLE base ADD COLUMN extra TEXT DEFAULT ''`)
			return err
		},
	})
	RegisterMigration(Migration{
		Version: "1.2.0", Schema: "multidb",
		Up: func(ctx context.Context) error {
			return errors.New("boom at 1.2.0")
		},
	})

	m := NewMigrator("multitool", "1.2.0")
	m.AddDriver(d)

	err = m.Run(context.Background())
	require.Error(t, err)

	// After restore: extra column should NOT exist.
	db2, openErr := sql.Open("sqlite", dbPath)
	require.NoError(t, openErr)
	defer db2.Close()

	var cnt int
	err = db2.QueryRow(
		`SELECT count(*) FROM pragma_table_info('base') WHERE name = 'extra'`,
	).Scan(&cnt)
	require.NoError(t, err)
	assert.Equal(t, 0, cnt, "extra column should not exist after restore")
}

func TestE2E_ManualRollbackRestores(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	dbPath := filepath.Join(tmp, "manual.db")
	d := newSQLiteTestDriver(t, "manualdb", "manualtool", dbPath)

	_, err := d.db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)`)
	require.NoError(t, err)
	_, err = d.db.Exec(`INSERT INTO items (name) VALUES ('original')`)
	require.NoError(t, err)

	// Set initial version.
	vDir := filepath.Join(tmp, "hop", "manualtool", "manualdb")
	require.NoError(t, os.MkdirAll(vDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(vDir, "version"), []byte("1.0.0"), 0o600))

	// Register migration that modifies schema then fails.
	RegisterMigration(Migration{
		Version: "1.1.0", Schema: "manualdb",
		Up: func(ctx context.Context) error {
			_, err := d.db.Exec(`ALTER TABLE items ADD COLUMN extra TEXT DEFAULT ''`)
			if err != nil {
				return err
			}
			return errors.New("simulated failure after schema change")
		},
	})

	// Manual rollback mode — no auto-rollback on failure.
	m := NewMigrator("manualtool", "1.1.0", WithManualRollback())
	m.AddDriver(d)

	err = m.Run(context.Background())
	require.Error(t, err, "migration should fail")

	// Explicitly call RollbackLatest to restore from backup.
	require.NoError(t, m.RollbackLatest(), "manual rollback should succeed")

	// Re-open DB to verify restored state.
	db2, openErr := sql.Open("sqlite", dbPath)
	require.NoError(t, openErr)
	defer db2.Close()

	var cnt int
	err = db2.QueryRow(
		`SELECT count(*) FROM pragma_table_info('items') WHERE name = 'extra'`,
	).Scan(&cnt)
	require.NoError(t, err)
	assert.Equal(t, 0, cnt, "extra column should not exist after manual rollback")

	// Version file should still be at pre-migration value.
	v, err := ReadVersionFile("manualtool", "manualdb")
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", v)
}

func TestE2E_PreReleaseOrdering(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	dbPath := filepath.Join(tmp, "prerel.db")
	d := newSQLiteTestDriver(t, "prereldb", "prereltool", dbPath)

	_, err := d.db.Exec(`CREATE TABLE data (id INTEGER PRIMARY KEY)`)
	require.NoError(t, err)

	// Set initial version.
	vDir := filepath.Join(tmp, "hop", "prereltool", "prereldb")
	require.NoError(t, os.MkdirAll(vDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(vDir, "version"), []byte("0.9.0"), 0o600))

	// Register pre-release + release migrations.
	var applied []string
	for _, ver := range []string{
		"1.0.0-alpha.1", "1.0.0-beta.1", "1.0.0-rc.1", "1.0.0",
	} {
		v := ver // capture
		RegisterMigration(Migration{
			Version: v, Schema: "prereldb",
			Up: func(ctx context.Context) error {
				applied = append(applied, v)
				return nil
			},
		})
	}

	m := NewMigrator("prereltool", "1.0.0")
	m.AddDriver(d)
	require.NoError(t, m.Run(context.Background()))

	// All 4 applied in correct semver order.
	require.Len(t, applied, 4)
	assert.Equal(t, []string{
		"1.0.0-alpha.1", "1.0.0-beta.1", "1.0.0-rc.1", "1.0.0",
	}, applied)

	// Verify version file.
	v, err := ReadVersionFile("prereltool", "prereldb")
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", v)
}
