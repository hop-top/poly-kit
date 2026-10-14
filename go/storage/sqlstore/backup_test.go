package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/storage/blob/local"
	"hop.top/kit/go/storage/sqldb"
)

// newTestDB creates a WAL-mode database at path holding table t with n
// rows and a meta row recording n, and closes it.
func newTestDB(t *testing.T, path string, n int) {
	t.Helper()
	db, err := sqldb.Open(sqldb.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`create table t (id integer primary key, v text not null);
		create table meta (k text primary key, n integer not null);
		insert into meta values ('rows', 0);`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		insertRow(t, db)
	}
}

// insertRow commits one row and bumps meta.n in the same transaction, so
// a consistent copy always has count(t) == meta.n.
func insertRow(t testing.TB, db *sql.DB) {
	t.Helper()
	if err := insertRowErr(db); err != nil {
		t.Fatal(err)
	}
}

func insertRowErr(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`insert into t (v) values (?)`, strings.Repeat("x", 512)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`update meta set n = n + 1 where k = 'rows'`); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// openWriter opens path with WAL and automatic checkpoints off, so the
// rows it commits stay in the -wal file while it is open.
func openWriter(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

// pinReader starts a read transaction on path and holds it until the
// test ends. SQLite cannot checkpoint WAL frames committed after a live
// reader's snapshot, so rows committed from here on stay WAL-only even
// if someone checkpoints.
func pinReader(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(`select count(*) from t`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback()
		_ = db.Close()
	})
}

// checkBackup opens the backup read-only, requires integrity_check ok and
// a transactionally consistent row count, and returns that count.
func checkBackup(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var ic string
	if err := db.QueryRow(`pragma integrity_check`).Scan(&ic); err != nil {
		t.Fatalf("integrity_check on backup: %v", err)
	}
	if ic != "ok" {
		t.Fatalf("integrity_check on backup = %q", ic)
	}
	var rows, meta int
	if err := db.QueryRow(`select count(*) from t`).Scan(&rows); err != nil {
		t.Fatalf("count backup rows: %v", err)
	}
	if err := db.QueryRow(`select n from meta where k = 'rows'`).Scan(&meta); err != nil {
		t.Fatalf("read backup meta: %v", err)
	}
	if rows != meta {
		t.Fatalf("backup not transactionally consistent: %d rows, meta says %d", rows, meta)
	}
	return rows
}

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %v, want %v", path, got, want)
	}
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func withClock(now func() time.Time) Option {
	return func(c *backupConfig) { c.now = now }
}

func TestBackupBeforeMigrate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	t.Run("no-op when file missing", func(t *testing.T) {
		got, err := BackupBeforeMigrate(dbPath, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Fatalf("expected empty path, got %q", got)
		}
		if _, err := os.Stat(dbPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("backup created the source: %v", err)
		}
	})

	newTestDB(t, dbPath, 5)

	t.Run("creates timestamped backup in default .dbs dir", func(t *testing.T) {
		got, err := BackupBeforeMigrate(dbPath, 7)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expectedDir := filepath.Join(dir, ".dbs")
		if filepath.Dir(got) != expectedDir {
			t.Fatalf("backup not in default .dbs dir: got %s, want under %s", got, expectedDir)
		}
		if !strings.HasPrefix(filepath.Base(got), "test.pre-v7.") {
			t.Fatalf("backup name missing version: %s", got)
		}
		if !strings.HasSuffix(got, ".bak") {
			t.Fatalf("backup missing .bak suffix: %s", got)
		}
		if n := checkBackup(t, got); n != 5 {
			t.Fatalf("backup has %d rows, want 5", n)
		}
		requireMode(t, got, 0o600)
		requireMode(t, expectedDir, 0o700)
		for _, name := range listDir(t, expectedDir) {
			if strings.HasSuffix(name, ".partial") {
				t.Fatalf("temporary file left behind: %s", name)
			}
		}
	})

	t.Run("does not modify source", func(t *testing.T) {
		if n := checkBackup(t, dbPath); n != 5 {
			t.Fatalf("source has %d rows, want 5", n)
		}
	})

	t.Run("WithBackupDir overrides destination", func(t *testing.T) {
		overrideDir := filepath.Join(t.TempDir(), "custom", "nested")
		got, err := BackupBeforeMigrate(dbPath, 9, WithBackupDir(overrideDir))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if filepath.Dir(got) != overrideDir {
			t.Fatalf("backup not in override dir: got %s, want under %s", got, overrideDir)
		}
		requireMode(t, overrideDir, 0o700)
		checkBackup(t, got)
	})

	t.Run("same-second backups never overwrite", func(t *testing.T) {
		out := t.TempDir()
		fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		clock := withClock(func() time.Time { return fixed })
		a, err := BackupBeforeMigrate(dbPath, 1, WithBackupDir(out), clock)
		if err != nil {
			t.Fatal(err)
		}
		b, err := BackupBeforeMigrate(dbPath, 1, WithBackupDir(out), clock)
		if err != nil {
			t.Fatal(err)
		}
		if a == b {
			t.Fatalf("second backup reused %s", a)
		}
		if filepath.Base(b) != "test.pre-v1.20260102-030405.1.bak" {
			t.Fatalf("collision name = %s", filepath.Base(b))
		}
	})

	t.Run("path needing URI escaping", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("'?' is not a valid file name character on Windows")
		}
		odd := filepath.Join(t.TempDir(), "we ird%20#?", "app #1?.db")
		if err := os.MkdirAll(filepath.Dir(odd), 0o700); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", sqlitePlainPathDSN(t, odd))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`create table t (id integer primary key, v text);
			create table meta (k text primary key, n integer); insert into meta values ('rows', 0)`); err != nil {
			t.Fatal(err)
		}
		insertRow(t, db)
		db.Close()
		got, err := BackupBeforeMigrate(odd, 1)
		if err != nil {
			t.Fatal(err)
		}
		if n := checkBackupAt(t, got); n != 1 {
			t.Fatalf("backup has %d rows, want 1", n)
		}
	})
}

// sqlitePlainPathDSN builds an escaped file: URI for an arbitrary path.
func sqlitePlainPathDSN(t *testing.T, p string) string {
	t.Helper()
	uri, err := readOnlyURI(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Replace(uri, "mode=ro", "mode=rwc", 1)
}

// checkBackupAt is checkBackup for a backup whose path holds URI
// metacharacters: it checks a copy at a plain path, since checkBackup
// opens an unescaped file: DSN.
func checkBackupAt(t *testing.T, path string) int {
	t.Helper()
	link := filepath.Join(t.TempDir(), "copy.db")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return checkBackup(t, link)
}

// TestBackupBeforeMigrate_WALOnlyRows: rows committed into the WAL and
// not yet checkpointed (a live reader blocks checkpointing them) must be
// in the backup. A copy of the main database file misses them.
func TestBackupBeforeMigrate_WALOnlyRows(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "app.db")
	newTestDB(t, dbPath, 3)

	w := openWriter(t, dbPath)
	pinReader(t, dbPath)
	for i := 0; i < 20; i++ {
		insertRow(t, w)
	}

	got, err := BackupBeforeMigrate(dbPath, 2)
	if err != nil {
		t.Fatalf("BackupBeforeMigrate: %v", err)
	}
	if n := checkBackup(t, got); n != 23 {
		t.Fatalf("backup has %d rows, want 23 (3 checkpointed + 20 WAL-only)", n)
	}
}

// TestBackupBeforeMigrate_ConcurrentWriter takes backups while another
// connection commits continuously. Each backup must open, pass
// integrity_check, be transactionally consistent, and hold every row
// committed before the backup started.
func TestBackupBeforeMigrate_ConcurrentWriter(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "app.db")
	newTestDB(t, dbPath, 10)

	w := openWriter(t, dbPath)
	pinReader(t, dbPath)

	var committed atomic.Int64
	committed.Store(10)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var writeErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := insertRowErr(w); err != nil {
				writeErr = err
				return
			}
			committed.Add(1)
		}
	}()

	// Let the writer get ahead of the last checkpoint.
	for committed.Load() < 200 {
		time.Sleep(time.Millisecond)
	}

	out := t.TempDir()
	for i := 0; i < 5; i++ {
		before := int(committed.Load())
		got, err := BackupBeforeMigrate(dbPath, i, WithBackupDir(out))
		if err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("backup %d: %v", i, err)
		}
		if n := checkBackup(t, got); n < before {
			close(stop)
			wg.Wait()
			t.Fatalf("backup %d has %d rows, but %d were committed before it started", i, n, before)
		}
	}
	close(stop)
	wg.Wait()
	if writeErr != nil {
		t.Fatalf("writer: %v", writeErr)
	}
}

func TestBackupBeforeMigrate_WithDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "app.db")
	newTestDB(t, dbPath, 2)
	w := openWriter(t, dbPath)
	pinReader(t, dbPath)
	for i := 0; i < 4; i++ {
		insertRow(t, w)
	}

	got, err := BackupBeforeMigrate(dbPath, 1, WithDB(w))
	if err != nil {
		t.Fatalf("BackupBeforeMigrate: %v", err)
	}
	if n := checkBackup(t, got); n != 6 {
		t.Fatalf("backup has %d rows, want 6", n)
	}
	requireMode(t, got, 0o600)
}

func TestBackupBeforeMigrate_Failures(t *testing.T) {
	t.Run("not a database", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "junk.db")
		if err := os.WriteFile(dbPath, []byte(strings.Repeat("not sqlite ", 64)), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := BackupBeforeMigrate(dbPath, 1)
		if err == nil {
			t.Fatalf("expected error, got backup %s", got)
		}
		if got != "" {
			t.Fatalf("failed backup returned path %q", got)
		}
		if names := listDir(t, filepath.Join(dir, ".dbs")); len(names) != 0 {
			t.Fatalf("failed backup left files: %v", names)
		}
	})

	t.Run("backup dir is a file", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "app.db")
		newTestDB(t, dbPath, 1)
		blocker := filepath.Join(dir, "blocker")
		if err := os.WriteFile(blocker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := BackupBeforeMigrate(dbPath, 1, WithBackupDir(blocker)); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "app.db")
		newTestDB(t, dbPath, 1)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := BackupBeforeMigrateContext(ctx, dbPath, 1); err == nil {
			t.Fatal("expected error")
		}
		if names := listDir(t, filepath.Join(dir, ".dbs")); len(names) != 0 {
			t.Fatalf("failed backup left files: %v", names)
		}
	})

	t.Run("naming returns a path", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "app.db")
		newTestDB(t, dbPath, 1)
		_, err := BackupBeforeMigrate(dbPath, 1, WithNaming(badNaming{}))
		if err == nil || !strings.Contains(err.Error(), "invalid backup name") {
			t.Fatalf("err = %v, want invalid backup name", err)
		}
		if names := listDir(t, filepath.Join(dir, ".dbs")); len(names) != 0 {
			t.Fatalf("failed backup left files: %v", names)
		}
	})
}

type badNaming struct{}

func (badNaming) Name(string, int, time.Time, int) string { return "../escape.bak" }
func (badNaming) Parse(string, string) (time.Time, int, bool) {
	return time.Time{}, 0, false
}

// migrateNaming mirrors a consumer's scheme:
// pre-migrate-v<from>-v<to>-<UTC>[-<n>].sqlite.
type migrateNaming struct{ from int }

const migrateLayout = "20060102T150405Z"

func (m migrateNaming) Name(_ string, to int, taken time.Time, attempt int) string {
	n := fmt.Sprintf("pre-migrate-v%d-v%d-%s", m.from, to, taken.UTC().Format(migrateLayout))
	if attempt > 0 {
		n += fmt.Sprintf("-%d", attempt)
	}
	return n + ".sqlite"
}

func (migrateNaming) Parse(_, name string) (time.Time, int, bool) {
	var from, to, attempt int
	var ts string
	rest, ok := strings.CutSuffix(name, ".sqlite")
	if !ok {
		return time.Time{}, 0, false
	}
	if _, err := fmt.Sscanf(rest, "pre-migrate-v%d-v%d-%s", &from, &to, &ts); err != nil {
		return time.Time{}, 0, false
	}
	if i := strings.IndexByte(ts, '-'); i >= 0 {
		var err error
		if _, err = fmt.Sscanf(ts[i+1:], "%d", &attempt); err != nil {
			return time.Time{}, 0, false
		}
		ts = ts[:i]
	}
	taken, err := time.Parse(migrateLayout, ts)
	if err != nil {
		return time.Time{}, 0, false
	}
	return taken, attempt, true
}

func TestBackupBeforeMigrate_Retention(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "app.db")
	newTestDB(t, dbPath, 1)
	out := filepath.Join(dir, "backups")

	// Files retention must never touch.
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"notes.txt", "other.pre-v1.20200101-000000.bak"} {
		if err := os.WriteFile(filepath.Join(out, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("default naming", func(t *testing.T) {
		base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		var made []string
		for i, at := range []time.Time{base, base.Add(time.Hour), base.Add(time.Hour), base.Add(2 * time.Hour)} {
			got, err := BackupBeforeMigrate(dbPath, i+1, WithBackupDir(out), WithKeep(2),
				withClock(func() time.Time { return at }))
			if err != nil {
				t.Fatalf("backup %d: %v", i, err)
			}
			made = append(made, filepath.Base(got))
		}
		want := map[string]bool{
			"notes.txt":                        true,
			"other.pre-v1.20200101-000000.bak": true,
			made[3]:                            true, // newest
			made[2]:                            true, // same second as made[1], later attempt
		}
		names := listDir(t, out)
		if len(names) != len(want) {
			t.Fatalf("after retention: %v, want %v", names, want)
		}
		for _, n := range names {
			if !want[n] {
				t.Fatalf("after retention: %v, want %v", names, want)
			}
		}
	})

	t.Run("custom naming", func(t *testing.T) {
		out := filepath.Join(dir, "custom")
		base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
		var made []string
		for i := 0; i < 5; i++ {
			at := base.Add(time.Duration(i) * time.Minute)
			got, err := BackupBeforeMigrate(dbPath, i+2, WithBackupDir(out), WithKeep(3),
				WithNaming(migrateNaming{from: i + 1}), withClock(func() time.Time { return at }))
			if err != nil {
				t.Fatalf("backup %d: %v", i, err)
			}
			made = append(made, filepath.Base(got))
		}
		if made[0] != "pre-migrate-v1-v2-20260401T000000Z.sqlite" {
			t.Fatalf("custom name = %s", made[0])
		}
		names := listDir(t, out)
		if strings.Join(names, ",") != strings.Join(made[2:], ",") {
			t.Fatalf("after retention: %v, want %v", names, made[2:])
		}
	})

	t.Run("prune failure keeps backup and wraps ErrRetention", func(t *testing.T) {
		out := filepath.Join(dir, "prune-fail")
		failRemove := func(c *backupConfig) {
			c.remove = func(string) error { return errors.New("boom") }
		}
		for i := 0; i < 2; i++ {
			at := time.Date(2026, 5, 1, 0, i, 0, 0, time.UTC)
			got, err := BackupBeforeMigrate(dbPath, 1, WithBackupDir(out), WithKeep(1), failRemove,
				withClock(func() time.Time { return at }))
			if i == 0 {
				if err != nil {
					t.Fatalf("first backup: %v", err)
				}
				continue
			}
			if !errors.Is(err, ErrRetention) {
				t.Fatalf("err = %v, want ErrRetention", err)
			}
			if got == "" {
				t.Fatal("retention failure dropped the backup path")
			}
			checkBackup(t, got)
		}
	})
}

func TestBackupRestoreRoundtrip(t *testing.T) {
	ctx := context.Background()
	dbDir := t.TempDir()
	blobDir := t.TempDir()

	dbPath := filepath.Join(dbDir, "app.db")
	newTestDB(t, dbPath, 3)
	w := openWriter(t, dbPath)
	pinReader(t, dbPath)
	for i := 0; i < 4; i++ {
		insertRow(t, w)
	}

	store, err := local.New(blobDir)
	if err != nil {
		t.Fatal(err)
	}
	key := "backups/app.db"

	if err := Backup(ctx, dbPath, store, key); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	ok, err := store.Exists(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("backup blob not found")
	}
	for _, name := range listDir(t, dbDir) {
		if strings.HasSuffix(name, ".partial") {
			t.Fatalf("temporary snapshot left beside the db: %s", name)
		}
	}

	restored := filepath.Join(t.TempDir(), "restored.db")
	if err := Restore(ctx, restored, store, key); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if n := checkBackup(t, restored); n != 7 {
		t.Fatalf("restored db has %d rows, want 7 (incl. WAL-only rows)", n)
	}
}

func TestBackupMissingSourceToStore(t *testing.T) {
	store, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = Backup(context.Background(), filepath.Join(t.TempDir(), "missing.db"), store, "k")
	if err == nil {
		t.Fatal("expected error for missing source")
	}
}

func TestBackupNilStoreFallback(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fallback.db")
	newTestDB(t, dbPath, 1)

	ctx := context.Background()
	if err := Backup(ctx, dbPath, nil, "unused"); err != nil {
		t.Fatalf("Backup with nil store: %v", err)
	}

	// Should have created a local .bak file under dir/.dbs/ (the default).
	found := ""
	for _, name := range listDir(t, filepath.Join(dir, ".dbs")) {
		if strings.HasSuffix(name, ".bak") {
			found = name
		}
	}
	if found == "" {
		t.Fatal("expected local .bak fallback under .dbs/")
	}
	checkBackup(t, filepath.Join(dir, ".dbs", found))
}

func TestRestoreNilStoreNoop(t *testing.T) {
	ctx := context.Background()
	err := Restore(ctx, "/nonexistent/path.db", nil, "key")
	if err != nil {
		t.Fatalf("Restore with nil store should be no-op: %v", err)
	}
}
