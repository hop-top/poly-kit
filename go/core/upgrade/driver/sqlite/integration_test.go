package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/upgrade"

	_ "modernc.org/sqlite"
)

// helper: create a real SQLite DB with a v1 schema.
func setupDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "app.db")
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO users (name) VALUES ('alice')`)
	require.NoError(t, err)

	return dbPath, db
}

func TestIntegration_MigrateV1toV2(t *testing.T) {
	upgrade.ResetRegistryForTest()
	defer upgrade.ResetRegistryForTest()

	dbPath, db := setupDB(t)
	tmp := filepath.Dir(dbPath)
	t.Setenv("XDG_DATA_HOME", tmp)

	d := New(dbPath, WithName("appdb"), WithTool("testcli"))

	// Register v1→v2 migration: add email column.
	upgrade.RegisterMigration(upgrade.Migration{
		Version: "1.1.0",
		Schema:  "appdb",
		Up: func(ctx context.Context) error {
			_, err := db.Exec(`ALTER TABLE users ADD COLUMN email TEXT DEFAULT ''`)
			return err
		},
		Down: func(ctx context.Context) error {
			// SQLite doesn't support DROP COLUMN in old versions;
			// for test purposes, just signal success.
			return nil
		},
	})

	// Set initial version.
	vDir := filepath.Join(tmp, "hop", "testcli", "appdb")
	require.NoError(t, os.MkdirAll(vDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(vDir, "version"), []byte("1.0.0"), 0o600))

	m := upgrade.NewMigrator("testcli", "1.1.0")
	m.AddDriver(d)

	err := m.Run(context.Background())
	require.NoError(t, err)

	// Verify schema changed: email column exists.
	var email string
	err = db.QueryRow(`SELECT email FROM users WHERE name = 'alice'`).Scan(&email)
	require.NoError(t, err)
	assert.Equal(t, "", email)

	// Verify version file updated.
	v, err := upgrade.ReadVersionFile("testcli", "appdb")
	require.NoError(t, err)
	assert.Equal(t, "1.1.0", v)
}

func TestIntegration_FailureRollbackRestoresState(t *testing.T) {
	upgrade.ResetRegistryForTest()
	defer upgrade.ResetRegistryForTest()

	dbPath, db := setupDB(t)
	tmp := filepath.Dir(dbPath)
	t.Setenv("XDG_DATA_HOME", tmp)

	d := New(dbPath, WithName("appdb"), WithTool("testcli"))

	// v1→v2 succeeds.
	upgrade.RegisterMigration(upgrade.Migration{
		Version: "1.1.0",
		Schema:  "appdb",
		Up: func(ctx context.Context) error {
			_, err := db.Exec(`ALTER TABLE users ADD COLUMN email TEXT DEFAULT ''`)
			return err
		},
	})
	// v2→v3 fails.
	upgrade.RegisterMigration(upgrade.Migration{
		Version: "1.2.0",
		Schema:  "appdb",
		Up: func(ctx context.Context) error {
			return errors.New("simulated failure")
		},
	})

	vDir := filepath.Join(tmp, "hop", "testcli", "appdb")
	require.NoError(t, os.MkdirAll(vDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(vDir, "version"), []byte("1.0.0"), 0o600))

	m := upgrade.NewMigrator("testcli", "1.2.0")
	m.AddDriver(d)

	err := m.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated failure")

	// After restore: email column should NOT exist (original v1 schema).
	// Reopen connection since restore replaced the file.
	db2, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db2.Close()

	var cnt int
	err = db2.QueryRow(
		`SELECT count(*) FROM pragma_table_info('users') WHERE name = 'email'`,
	).Scan(&cnt)
	require.NoError(t, err)
	assert.Equal(t, 0, cnt, "email column should not exist after rollback")

	// Version file should be unchanged.
	v, err := upgrade.ReadVersionFile("testcli", "appdb")
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", v)
}

func TestIntegration_BackupFileExists(t *testing.T) {
	upgrade.ResetRegistryForTest()
	defer upgrade.ResetRegistryForTest()

	dbPath, db := setupDB(t)
	_ = db
	tmp := filepath.Dir(dbPath)
	t.Setenv("XDG_DATA_HOME", tmp)

	d := New(dbPath, WithName("appdb"), WithTool("testcli"))

	upgrade.RegisterMigration(upgrade.Migration{
		Version: "1.1.0",
		Schema:  "appdb",
		Up:      func(ctx context.Context) error { return nil },
	})

	vDir := filepath.Join(tmp, "hop", "testcli", "appdb")
	require.NoError(t, os.MkdirAll(vDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(vDir, "version"), []byte("1.0.0"), 0o600))

	m := upgrade.NewMigrator("testcli", "1.1.0")
	m.AddDriver(d)

	require.NoError(t, m.Run(context.Background()))

	// Verify backup directory exists.
	backupBase := filepath.Join(tmp, "hop", "testcli", "backups", "appdb")
	entries, err := os.ReadDir(backupBase)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "backup directory should contain at least one entry")

	// Verify backup contains the db file.
	backupDB := filepath.Join(backupBase, entries[0].Name(), "app.db")
	_, err = os.Stat(backupDB)
	require.NoError(t, err)
}

func TestIntegration_RetentionPrunes(t *testing.T) {
	upgrade.ResetRegistryForTest()
	defer upgrade.ResetRegistryForTest()

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	dbPath := filepath.Join(tmp, "app.db")
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE t (id INTEGER)`)
	require.NoError(t, err)

	d := New(dbPath, WithName("appdb"), WithTool("testcli"))

	// Pre-create old backup dirs.
	backupBase := filepath.Join(tmp, "hop", "testcli", "backups", "appdb")
	for _, v := range []string{"pre-0.1.0", "pre-0.2.0", "pre-0.3.0", "pre-0.4.0"} {
		require.NoError(t, os.MkdirAll(filepath.Join(backupBase, v), 0o750))
	}

	upgrade.RegisterMigration(upgrade.Migration{
		Version: "1.1.0",
		Schema:  "appdb",
		Up:      func(ctx context.Context) error { return nil },
	})

	vDir := filepath.Join(tmp, "hop", "testcli", "appdb")
	require.NoError(t, os.MkdirAll(vDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(vDir, "version"), []byte("1.0.0"), 0o600))

	m := upgrade.NewMigrator("testcli", "1.1.0", upgrade.WithBackupRetention(3))
	m.AddDriver(d)

	require.NoError(t, m.Run(context.Background()))

	entries, err := os.ReadDir(backupBase)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(entries), 3, "should have pruned to retention limit")
}
