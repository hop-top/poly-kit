package sqlstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"hop.top/kit/go/storage/blob"
	"hop.top/kit/go/storage/blob/local"
)

// backupBlob writes a database with n rows, backs it up into a fresh
// local blob store and returns the store and key.
func backupBlob(t *testing.T, n int) (blob.Store, string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src.db")
	newTestDB(t, src, n)
	store, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const key = "backups/src.db"
	if err := Backup(context.Background(), src, store, key); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	return store, key
}

// staleWALTarget writes a database at dbPath with base checkpointed rows
// and extra more committed into the WAL, then leaves a -wal and -shm on
// disk the way a crashed or still-open writer would. Replaying that WAL
// onto any other database file gives that file this database's pages.
func staleWALTarget(t *testing.T, dbPath string, base, extra int) {
	t.Helper()
	newTestDB(t, dbPath, base)
	w := openWriter(t, dbPath)
	for i := 0; i < extra; i++ {
		insertRow(t, w)
	}
	wal, err := os.ReadFile(dbPath + "-wal")
	if err != nil {
		t.Fatalf("read wal: %v", err)
	}
	if len(wal) == 0 {
		t.Fatal("writer left an empty wal")
	}
	shm, err := os.ReadFile(dbPath + "-shm")
	if err != nil {
		t.Fatalf("read shm: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// Closing the last connection checkpoints and deletes the WAL; put
	// it back.
	if err := os.WriteFile(dbPath+"-wal", wal, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath+"-shm", shm, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath+"-journal", []byte("stale journal"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// snapshotFiles reads dbPath and its SQLite siblings; a missing file
// maps to nil.
func snapshotFiles(t *testing.T, dbPath string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm", dbPath + "-journal"} {
		b, err := os.ReadFile(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		out[p] = b
	}
	return out
}

func requireUnchanged(t *testing.T, before map[string][]byte, dbPath string) {
	t.Helper()
	after := snapshotFiles(t, dbPath)
	for p, b := range before {
		if !bytes.Equal(b, after[p]) {
			t.Fatalf("%s changed by a failed restore (%d bytes -> %d)", filepath.Base(p), len(b), len(after[p]))
		}
	}
	for _, name := range listDir(t, filepath.Dir(dbPath)) {
		if strings.Contains(name, ".restore") {
			t.Fatalf("temporary file left behind: %s", name)
		}
	}
}

func TestRestore_StaleWALNotReplayed(t *testing.T) {
	store, key := backupBlob(t, 5)
	dbPath := filepath.Join(t.TempDir(), "app.db")
	staleWALTarget(t, dbPath, 3, 40)

	if err := Restore(context.Background(), dbPath, store, key); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	// Open read-write, as the application would: that is the open that
	// replays a WAL found beside the file.
	if n := countRows(t, dbPath); n != 5 {
		t.Fatalf("restored db has %d rows, want 5 (the backup's, no WAL replay)", n)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(dbPath + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale %s left beside the restored db: %v", suffix, err)
		}
	}
	if n := checkBackup(t, dbPath); n != 5 {
		t.Fatalf("restored db has %d rows, want 5", n)
	}
}

// countRows opens path read-write, requires integrity_check ok and
// returns the row count of t, closing the database again.
func countRows(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var ic string
	if err := db.QueryRow(`pragma integrity_check`).Scan(&ic); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if ic != "ok" {
		t.Fatalf("integrity_check = %q", ic)
	}
	var n int
	if err := db.QueryRow(`select count(*) from t`).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func TestRestore_NewTarget(t *testing.T) {
	store, key := backupBlob(t, 2)
	dbPath := filepath.Join(t.TempDir(), "nested", "app.db")
	if err := Restore(context.Background(), dbPath, store, key); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if n := checkBackup(t, dbPath); n != 2 {
		t.Fatalf("restored db has %d rows, want 2", n)
	}
	requireMode(t, dbPath, 0o600)
}

func TestRestore_KeepsTargetMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	store, key := backupBlob(t, 1)
	dbPath := filepath.Join(t.TempDir(), "app.db")
	newTestDB(t, dbPath, 1)
	if err := os.Chmod(dbPath, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Restore(context.Background(), dbPath, store, key); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	requireMode(t, dbPath, 0o640)
}

// failingStore serves a blob whose reader fails after a few bytes.
type failingStore struct{ blob.Store }

func (failingStore) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(io.MultiReader(strings.NewReader("SQLite format 3\x00"),
		errReader{errors.New("connection reset")})), nil
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestRestore_FailureLeavesOriginal(t *testing.T) {
	t.Run("download fails", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "app.db")
		staleWALTarget(t, dbPath, 3, 10)
		before := snapshotFiles(t, dbPath)
		err := Restore(context.Background(), dbPath, failingStore{}, "k")
		if err == nil || !strings.Contains(err.Error(), "connection reset") {
			t.Fatalf("err = %v, want the read error", err)
		}
		requireUnchanged(t, before, dbPath)
	})

	t.Run("rename fails", func(t *testing.T) {
		store, key := backupBlob(t, 5)
		dbPath := filepath.Join(t.TempDir(), "app.db")
		staleWALTarget(t, dbPath, 3, 10)
		before := snapshotFiles(t, dbPath)
		orig := restoreRename
		restoreRename = func(string, string) error { return errors.New("injected rename failure") }
		t.Cleanup(func() { restoreRename = orig })
		err := Restore(context.Background(), dbPath, store, key)
		if err == nil || !strings.Contains(err.Error(), "injected rename failure") {
			t.Fatalf("err = %v, want the rename error", err)
		}
		requireUnchanged(t, before, dbPath)
	})

	t.Run("blob missing", func(t *testing.T) {
		store, _ := backupBlob(t, 1)
		dbPath := filepath.Join(t.TempDir(), "app.db")
		staleWALTarget(t, dbPath, 3, 10)
		before := snapshotFiles(t, dbPath)
		if err := Restore(context.Background(), dbPath, store, "missing"); err == nil {
			t.Fatal("expected error")
		}
		requireUnchanged(t, before, dbPath)
	})
}

// TestRestore_DirSync: the directory is fsynced after the rename and
// the sibling removals, so all three survive a crash together; an fsync
// failure is reported.
func TestRestore_DirSync(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directories are not fsynced on Windows")
	}
	store, key := backupBlob(t, 4)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "app.db")
	staleWALTarget(t, dbPath, 2, 10)

	orig := restoreSyncDir
	t.Cleanup(func() { restoreSyncDir = orig })
	var synced []string
	restoreSyncDir = func(p string) error {
		synced = append(synced, p)
		if _, err := os.Stat(dbPath + "-wal"); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("dir fsynced before the stale wal was removed: %v", err)
		}
		return orig(p)
	}
	if err := Restore(context.Background(), dbPath, store, key); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(synced) != 1 || synced[0] != dir {
		t.Fatalf("dir fsyncs = %v, want [%s]", synced, dir)
	}

	restoreSyncDir = func(string) error { return errors.New("injected fsync failure") }
	err := Restore(context.Background(), dbPath, store, key)
	if err == nil || !strings.Contains(err.Error(), "injected fsync failure") {
		t.Fatalf("err = %v, want the fsync error", err)
	}
}

// TestRestore_SidecarRemoveFails: the backup is in place, but a stale
// sidecar that could not be removed is named in the error, since opening
// the database before removing it would replay it.
func TestRestore_SidecarRemoveFails(t *testing.T) {
	store, key := backupBlob(t, 5)
	dbPath := filepath.Join(t.TempDir(), "app.db")
	staleWALTarget(t, dbPath, 3, 10)

	orig := restoreRemove
	t.Cleanup(func() { restoreRemove = orig })
	restoreRemove = func(p string) error {
		if strings.HasSuffix(p, "-wal") {
			return errors.New("injected remove failure")
		}
		return orig(p)
	}
	err := Restore(context.Background(), dbPath, store, key)
	if err == nil || !strings.Contains(err.Error(), dbPath+"-wal") ||
		!strings.Contains(err.Error(), "injected remove failure") {
		t.Fatalf("err = %v, want the stale -wal named", err)
	}
	if _, err := os.Stat(dbPath + "-shm"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("other sidecars not removed: %v", err)
	}
	if err := os.Remove(dbPath + "-wal"); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, dbPath); n != 5 {
		t.Fatalf("restored db has %d rows, want 5", n)
	}
}
