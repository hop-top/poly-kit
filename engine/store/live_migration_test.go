package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/storage/sqldb"
)

// TestLiveMigration_FreshBoot: a fresh DB has the `live` column
// from the CREATE TABLE in versionTablesSQL; migrateAddLiveColumn
// is a no-op.
func TestLiveMigration_FreshBoot(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "kit.db")

	ds, err := NewDocumentStore(dbPath)
	require.NoError(t, err)
	defer ds.Close()

	// Verify the live column exists.
	assert.True(t, hasColumn(t, ds.DB(), "versions", "live"),
		"fresh boot: versions.live column present")
}

// TestLiveMigration_LegacyBoot: a legacy DB without the `live`
// column gets it added by migrateAddLiveColumn. Pre-existing rows
// have live=1 (the DEFAULT).
func TestLiveMigration_LegacyBoot(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	// Step 1: open a "legacy" DB by hand, creating the versions table
	// WITHOUT the live column. Use sqldb.Open so the modernc.org/sqlite
	// driver is registered (matching what NewDocumentStore uses on
	// the second open below). Close after seeding.
	legacy, err := sqldb.Open(sqldb.Options{Path: dbPath})
	require.NoError(t, err)
	_, err = legacy.Exec(createTableSQL)
	require.NoError(t, err)
	_, err = legacy.Exec(`
CREATE TABLE versions (
	type        TEXT    NOT NULL,
	id          TEXT    NOT NULL,
	version_id  TEXT    NOT NULL,
	seq         INTEGER NOT NULL,
	hash        TEXT    NOT NULL,
	timestamp   INTEGER NOT NULL,
	created_at  TEXT    NOT NULL,
	PRIMARY KEY (type, id, seq),
	UNIQUE (version_id)
);
CREATE TABLE version_parents (
	version_id TEXT NOT NULL,
	parent_id  TEXT NOT NULL,
	PRIMARY KEY (version_id, parent_id),
	FOREIGN KEY (version_id) REFERENCES versions(version_id) ON DELETE CASCADE,
	FOREIGN KEY (parent_id)  REFERENCES versions(version_id)
);
CREATE TABLE snapshot_blobs (
	hash      TEXT    NOT NULL PRIMARY KEY,
	data      BLOB    NOT NULL,
	refcount  INTEGER NOT NULL CHECK (refcount >= 0)
);
CREATE TABLE version_snapshots (
	version_id  TEXT NOT NULL PRIMARY KEY,
	hash        TEXT NOT NULL,
	FOREIGN KEY (version_id) REFERENCES versions(version_id) ON DELETE CASCADE,
	FOREIGN KEY (hash)       REFERENCES snapshot_blobs(hash)
);`)
	require.NoError(t, err)

	// Insert a pre-existing row so we can verify the DEFAULT lands
	// after the migration.
	_, err = legacy.Exec(
		`INSERT INTO versions (type, id, version_id, seq, hash, timestamp, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"note", "n1", "v_legacy", 1, "h1", int64(0), "2026-01-01T00:00:00Z",
	)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())

	// Step 2: open through NewDocumentStore. The versionTablesSQL
	// CREATE TABLE IF NOT EXISTS is a no-op (table exists), so the
	// CREATE doesn't add the column. migrateAddLiveColumn detects
	// the missing column and runs the ALTER.
	ds, err := NewDocumentStore(dbPath)
	require.NoError(t, err)
	defer ds.Close()

	assert.True(t, hasColumn(t, ds.DB(), "versions", "live"),
		"legacy boot: versions.live column added by migration")

	// Pre-existing rows have live=1 (DEFAULT applied by ALTER).
	var live int
	err = ds.DB().QueryRow(
		`SELECT live FROM versions WHERE version_id = ?`, "v_legacy",
	).Scan(&live)
	require.NoError(t, err)
	assert.Equal(t, 1, live, "pre-existing row has live=1 after ALTER")
}

// TestLiveMigration_ReBoot: re-opening a post-migration DB is a
// no-op (the column is already present; migrateAddLiveColumn skips
// the ALTER).
func TestLiveMigration_ReBoot(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "kit.db")

	// First boot creates the column.
	ds1, err := NewDocumentStore(dbPath)
	require.NoError(t, err)
	require.NoError(t, ds1.Close())

	// Second boot: migration should be a no-op (the column is there).
	ds2, err := NewDocumentStore(dbPath)
	require.NoError(t, err)
	defer ds2.Close()

	assert.True(t, hasColumn(t, ds2.DB(), "versions", "live"),
		"re-boot: column still present, migration was a no-op")
}

// hasColumn reports whether the given table has the named column.
// Uses PRAGMA table_info.
func hasColumn(t *testing.T, db *sql.DB, table, col string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var (
			cid       int
			name      string
			colType   string
			notnull   int
			dfltValue sql.NullString
			pk        int
		)
		require.NoError(t, rows.Scan(&cid, &name, &colType, &notnull, &dfltValue, &pk))
		if name == col {
			return true
		}
	}
	require.NoError(t, rows.Err())
	return false
}
