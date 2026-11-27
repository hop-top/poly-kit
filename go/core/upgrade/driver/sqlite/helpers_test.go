package sqlite

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// openWAL opens (creating) a WAL-mode database with table t(x) and
// returns a one-connection handle. The handle stays open, so committed
// rows stay in the -wal file until a checkpoint.
func openWAL(t *testing.T, path string) *sql.DB {
	t.Helper()
	esc := (&url.URL{Path: path}).EscapedPath()
	db, err := sql.Open("sqlite", fmt.Sprintf(
		"file:%s?mode=rwc&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", esc))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS t (x INTEGER)`)
	require.NoError(t, err)
	return db
}

func insert(t *testing.T, db *sql.DB, xs ...int) {
	t.Helper()
	for _, x := range xs {
		_, err := db.Exec(`INSERT INTO t (x) VALUES (?)`, x)
		require.NoError(t, err)
	}
}

// rows opens the database file at path on its own connection, reads
// t(x) in order, and closes the connection.
func rows(t *testing.T, path string) []int {
	t.Helper()
	db, err := sql.Open("sqlite", fileDSN(path))
	require.NoError(t, err)
	defer db.Close()
	r, err := db.Query(`SELECT x FROM t ORDER BY x`)
	require.NoError(t, err)
	defer r.Close()
	var out []int
	for r.Next() {
		var x int
		require.NoError(t, r.Scan(&x))
		out = append(out, x)
	}
	require.NoError(t, r.Err())
	return out
}

func requireIntegrity(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", fileDSN(path))
	require.NoError(t, err)
	defer db.Close()
	var res string
	require.NoError(t, db.QueryRow(`PRAGMA integrity_check`).Scan(&res))
	require.Equal(t, "ok", res)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func sidecarPaths(db string) []string {
	return []string{db + "-wal", db + "-shm", db + "-journal"}
}

// copyFile copies src to dst (both plain files).
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dst, b, 0o600))
}

func join(elem ...string) string { return filepath.Join(elem...) }
