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

// TestDefaultSynchronousIsDurable pins the production default. The
// Synchronous option exists so a throwaway test database can drop the
// fsync per commit (engine/store's property tests); this asserts the
// option is opt-in, so no production caller — every one of which
// leaves Synchronous empty — silently loses crash durability.
func TestDefaultSynchronousIsDurable(t *testing.T) {
	db, err := sqldb.Open(sqldb.Options{Path: filepath.Join(t.TempDir(), "durable.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// SQLite reports synchronous as an int: 0=OFF, 1=NORMAL, 2=FULL,
	// 3=EXTRA. Anything but 0 keeps the commit-time flush.
	var mode int
	if err := db.QueryRow("PRAGMA synchronous").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode == 0 {
		t.Fatal("default synchronous is OFF: production databases lost crash durability")
	}

	// WAL is the other half of the durability default and travels in
	// the same DSN; a regression in one usually means both.
	var journal string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" {
		t.Fatalf("got journal_mode=%q, want wal", journal)
	}
}

// TestSynchronousOff covers the test-database path: the pragma must
// actually reach every pooled connection, not just be accepted.
func TestSynchronousOff(t *testing.T) {
	db, err := sqldb.Open(sqldb.Options{
		Path:        filepath.Join(t.TempDir(), "fast.db"),
		Synchronous: "OFF",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var mode int
	if err := db.QueryRow("PRAGMA synchronous").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != 0 {
		t.Fatalf("got synchronous=%d, want 0 (OFF)", mode)
	}

	// Journal mode is untouched by the option: the property tests rely
	// on WAL's cross-connection semantics matching production.
	var journal string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" {
		t.Fatalf("got journal_mode=%q, want wal", journal)
	}
}

// TestSynchronousRejectsUnknownMode guards the allowlist. A typo must
// fail the open rather than leave a test believing fsync was off.
func TestSynchronousRejectsUnknownMode(t *testing.T) {
	for _, mode := range []string{"NORMAL", "FULL", "off; DROP TABLE x", "0"} {
		if _, err := sqldb.Open(sqldb.Options{
			Path:        filepath.Join(t.TempDir(), "bad.db"),
			Synchronous: mode,
		}); err == nil {
			t.Fatalf("Synchronous=%q: want error, got nil", mode)
		}
	}
}
