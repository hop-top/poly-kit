package sqldb_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"hop.top/kit/go/storage/sqldb"
)

const openHelperEnv = "SQLDB_OPEN_HELPER"

// TestOpenHelperProcess is the child half of
// TestConcurrentOpenAcrossProcesses; it is a no-op in a normal run.
func TestOpenHelperProcess(t *testing.T) {
	path := os.Getenv(openHelperEnv)
	if path == "" {
		t.Skip("helper process only")
	}
	db, err := sqldb.Open(sqldb.Options{Path: path})
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(3)
	}
	_ = db.Close()
}

// prepareRollbackJournalDB creates a DB and leaves it in rollback-journal
// mode, the state an older file is in before its first WAL switch.
func prepareRollbackJournalDB(t *testing.T, path string) {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("prepare raw: %v", err)
	}
	defer raw.Close()
	for _, q := range []string{
		"PRAGMA journal_mode=DELETE",
		"CREATE TABLE IF NOT EXISTS t (id INTEGER)",
	} {
		if _, err := raw.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("prepare %q: %v", q, err)
		}
	}
}

// holdWriteLock takes an exclusive lock on path and releases it after hold.
// The returned channel closes once released.
func holdWriteLock(t *testing.T, path string, hold time.Duration) <-chan struct{} {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	conn, err := raw.Conn(context.Background())
	if err != nil {
		t.Fatalf("holder conn: %v", err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("holder begin: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(hold)
		_, _ = conn.ExecContext(context.Background(), "COMMIT")
		_ = conn.Close()
		_ = raw.Close()
	}()
	return done
}

// TestConcurrentOpenWaitsForWriter: goroutines opening a rollback-journal
// DB while a writer holds the lock all succeed once it releases.
func TestConcurrentOpenWaitsForWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	prepareRollbackJournalDB(t, path)
	done := holdWriteLock(t, path, 700*time.Millisecond)

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, err := sqldb.Open(sqldb.Options{Path: path})
			if err != nil {
				errs <- err
				return
			}
			_ = db.Close()
		}()
	}
	wg.Wait()
	<-done
	close(errs)
	for err := range errs {
		t.Errorf("open failed while writer held lock: %v", err)
	}
}

// TestConcurrentOpenAcrossProcesses: same as above with separate OS
// processes, the shape of parallel CLI invocations.
func TestConcurrentOpenAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	prepareRollbackJournalDB(t, path)
	done := holdWriteLock(t, path, 1500*time.Millisecond)

	const n = 8
	type result struct {
		out []byte
		err error
	}
	results := make(chan result, n)
	for range n {
		go func() {
			cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestOpenHelperProcess$")
			cmd.Env = append(os.Environ(), openHelperEnv+"="+path)
			out, err := cmd.CombinedOutput()
			results <- result{out, err}
		}()
	}
	for range n {
		if r := <-results; r.err != nil {
			t.Errorf("child open failed: %v\n%s", r.err, r.out)
		}
	}
	<-done
}

// TestOpenGivesUpAtBusyTimeout: a lock outlasting BusyTimeout makes Open
// fail near that deadline with a busy error, not hang or return early.
func TestOpenGivesUpAtBusyTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	prepareRollbackJournalDB(t, path)
	done := holdWriteLock(t, path, 3*time.Second)
	defer func() { <-done }()

	start := time.Now()
	db, err := sqldb.Open(sqldb.Options{Path: path, BusyTimeout: 300})
	elapsed := time.Since(start)
	if err == nil {
		_ = db.Close()
		t.Fatal("Open succeeded while lock held past BusyTimeout")
	}
	if elapsed < 300*time.Millisecond {
		t.Errorf("gave up after %v, before the 300ms BusyTimeout", elapsed)
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("gave up after %v, well past the 300ms BusyTimeout", elapsed)
	}
}
