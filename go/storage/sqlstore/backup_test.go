package sqlstore

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/go/storage/blob/local"
)

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
	})

	// Create a DB file with known content.
	content := []byte("sqlite-data-here")
	if err := os.WriteFile(dbPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("creates timestamped backup in default .dbs dir", func(t *testing.T) {
		got, err := BackupBeforeMigrate(dbPath, 7)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == "" {
			t.Fatal("expected backup path")
		}
		expectedDir := filepath.Join(dir, ".dbs")
		if filepath.Dir(got) != expectedDir {
			t.Fatalf("backup not in default .dbs dir: got %s, want under %s", got, expectedDir)
		}
		if _, err := os.Stat(expectedDir); err != nil {
			t.Fatalf("expected .dbs dir to exist: %v", err)
		}
		if !strings.Contains(got, "test.pre-v7.") {
			t.Fatalf("backup name missing version: %s", got)
		}
		if !strings.HasSuffix(got, ".bak") {
			t.Fatalf("backup missing .bak suffix: %s", got)
		}

		data, err := os.ReadFile(got)
		if err != nil {
			t.Fatalf("read backup: %v", err)
		}
		if string(data) != string(content) {
			t.Fatalf("backup content mismatch")
		}
	})

	t.Run("does not overwrite source", func(t *testing.T) {
		data, _ := os.ReadFile(dbPath)
		if string(data) != string(content) {
			t.Fatal("source was modified")
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
		if _, err := os.Stat(overrideDir); err != nil {
			t.Fatalf("override dir not created: %v", err)
		}
	})
}

func TestBackupRestoreRoundtrip(t *testing.T) {
	ctx := context.Background()
	dbDir := t.TempDir()
	blobDir := t.TempDir()

	dbPath := filepath.Join(dbDir, "app.db")
	content := []byte("sqlite3-database-content")
	if err := os.WriteFile(dbPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := local.New(blobDir)
	if err != nil {
		t.Fatal(err)
	}

	key := "backups/app.db"

	// Backup to blob store.
	if err := Backup(ctx, dbPath, store, key); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// Verify blob exists.
	ok, err := store.Exists(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("backup blob not found")
	}

	// Verify blob content matches original.
	rc, err := store.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != string(content) {
		t.Fatal("blob content mismatch")
	}

	// Delete original, then restore.
	os.Remove(dbPath)

	if err := Restore(ctx, dbPath, store, key); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	restored, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if string(restored) != string(content) {
		t.Fatal("restored content mismatch")
	}
}

func TestBackupNilStoreFallback(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fallback.db")
	content := []byte("data")
	if err := os.WriteFile(dbPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := Backup(ctx, dbPath, nil, "unused"); err != nil {
		t.Fatalf("Backup with nil store: %v", err)
	}

	// Should have created a local .bak file under dir/.dbs/ (the new default).
	entries, _ := os.ReadDir(filepath.Join(dir, ".dbs"))
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".bak") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected local .bak fallback under .dbs/")
	}
}

func TestRestoreNilStoreNoop(t *testing.T) {
	ctx := context.Background()
	err := Restore(ctx, "/nonexistent/path.db", nil, "key")
	if err != nil {
		t.Fatalf("Restore with nil store should be no-op: %v", err)
	}
}
