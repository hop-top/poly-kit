package sqldb_test

import (
	"os"
	"path/filepath"
	"testing"

	"hop.top/kit/go/storage/sqldb"
)

func TestOpenCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "test.db")

	db, err := sqldb.Open(sqldb.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("db file not created: %v", err)
	}
}

func TestOpenInMemory(t *testing.T) {
	db, err := sqldb.Open(sqldb.Options{Path: ":memory:"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var result string
	if err := db.QueryRow("SELECT 1").Scan(&result); err != nil {
		t.Fatal(err)
	}
}

func TestWALMode(t *testing.T) {
	db, err := sqldb.Open(sqldb.Options{Path: filepath.Join(t.TempDir(), "wal.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("got journal_mode=%q, want wal", mode)
	}
}

func TestBusyTimeout(t *testing.T) {
	db, err := sqldb.Open(sqldb.Options{
		Path:        filepath.Join(t.TempDir(), "bt.db"),
		BusyTimeout: 3000,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var timeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != 3000 {
		t.Fatalf("got busy_timeout=%d, want 3000", timeout)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	db, err := sqldb.Open(sqldb.Options{Path: ":memory:"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	migrations := map[int]string{
		1: "CREATE TABLE items (id TEXT PRIMARY KEY)",
		2: "ALTER TABLE items ADD COLUMN name TEXT",
	}

	if err := sqldb.Migrate(db, "schema_versions", migrations); err != nil {
		t.Fatal(err)
	}
	// Run again — must be idempotent.
	if err := sqldb.Migrate(db, "schema_versions", migrations); err != nil {
		t.Fatal(err)
	}

	// Verify table works.
	if _, err := db.Exec("INSERT INTO items (id, name) VALUES ('a', 'Alice')"); err != nil {
		t.Fatal(err)
	}
}

func TestMustOpenPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	sqldb.MustOpen(sqldb.Options{Path: ""})
}
