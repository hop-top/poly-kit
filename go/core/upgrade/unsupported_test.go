package upgrade

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unsupportedDriver is a driver whose backup or restore is unsupported.
type unsupportedDriver struct {
	version      string
	backupUnsup  bool
	restoreUnsup bool
	restoreCalls int
	unlockCalls  int
}

func (d *unsupportedDriver) Name() string             { return "db" }
func (d *unsupportedDriver) Version() (string, error) { return d.version, nil }
func (d *unsupportedDriver) SetVersion(v string) error {
	d.version = v
	return nil
}
func (d *unsupportedDriver) Backup(string) error {
	if d.backupUnsup {
		return ErrBackupUnsupported
	}
	return nil
}
func (d *unsupportedDriver) Restore(string) error {
	d.restoreCalls++
	if d.restoreUnsup {
		return ErrBackupUnsupported
	}
	return nil
}

type lockedUnsupported struct{ *unsupportedDriver }

func (d lockedUnsupported) Lock(context.Context) (func() error, error) {
	return func() error { d.unlockCalls++; return nil }, nil
}

// unsupportedMigrator targets 1.3.0 over migrations 1.1.0 (with a Down),
// 1.2.0 (none) and 1.3.0 (fails), so a failure needs a restore.
func unsupportedMigrator(t *testing.T, d SchemaDriver, opts ...MigratorOption) (*Migrator, string) {
	t.Helper()
	dir := t.TempDir()
	reg := NewRegistry()
	ok := func(context.Context) error { return nil }
	reg.Register(Migration{Version: "1.1.0", Schema: "db", Up: ok, Down: ok})
	reg.Register(Migration{Version: "1.2.0", Schema: "db", Up: ok})
	reg.Register(Migration{Version: "1.3.0", Schema: "db",
		Up: func(context.Context) error { return errors.New("boom") }})
	m := NewMigrator("unsup", "1.3.0", append([]MigratorOption{WithRegistry(reg), WithDataDir(dir)}, opts...)...)
	m.AddDriver(d)
	return m, dir
}

func TestRunNotRestoredMatchesSentinels(t *testing.T) {
	d := &unsupportedDriver{version: "1.0.0", backupUnsup: true}
	m, _ := unsupportedMigrator(t, d)
	err := m.Run(context.Background())
	if !errors.Is(err, ErrNotRestored) {
		t.Fatalf("err = %v, want ErrNotRestored", err)
	}
	if !errors.Is(err, ErrBackupUnsupported) {
		t.Fatalf("err = %v, want ErrBackupUnsupported as the cause", err)
	}
	if !strings.Contains(err.Error(), "up 1.3.0 failed: boom") {
		t.Fatalf("err = %v, want the failure that started the rollback", err)
	}
	if d.restoreCalls != 0 {
		t.Fatalf("Restore called %d times with no backup", d.restoreCalls)
	}
	if d.version != "1.2.0" {
		t.Fatalf("version = %q, want 1.2.0", d.version)
	}
}

func TestRunUnsupportedRestoreIsNotRestored(t *testing.T) {
	d := &unsupportedDriver{version: "1.0.0", restoreUnsup: true}
	m, _ := unsupportedMigrator(t, d)
	err := m.Run(context.Background())
	if !errors.Is(err, ErrNotRestored) || !errors.Is(err, ErrBackupUnsupported) {
		t.Fatalf("err = %v, want ErrNotRestored caused by ErrBackupUnsupported", err)
	}
	if d.restoreCalls != 1 {
		t.Fatalf("Restore called %d times, want 1", d.restoreCalls)
	}
}

// A restore that fails for another reason keeps its own error and is
// not reported as not restored.
func TestRunPlainRestoreFailureIsNotNotRestored(t *testing.T) {
	d := &failingRestore{unsupportedDriver{version: "1.0.0"}}
	m, _ := unsupportedMigrator(t, d)
	err := m.Run(context.Background())
	if errors.Is(err, ErrNotRestored) {
		t.Fatalf("err = %v, must not match ErrNotRestored", err)
	}
	if !strings.Contains(err.Error(), "disk gone") {
		t.Fatalf("err = %v", err)
	}
}

type failingRestore struct{ unsupportedDriver }

func (d *failingRestore) Restore(string) error { return errors.New("disk gone") }

func listNames(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

func TestUnsupportedBackupLeavesNothingAndSkipsPrune(t *testing.T) {
	d := &unsupportedDriver{version: "1.0.0", backupUnsup: true}
	dir := t.TempDir()
	reg := NewRegistry()
	reg.Register(Migration{Version: "1.1.0", Schema: "db", Up: func(context.Context) error { return nil }})
	m := NewMigrator("unsup", "1.1.0", WithRegistry(reg), WithDataDir(dir), WithBackupRetention(1))
	m.AddDriver(d)
	backups := filepath.Join(dir, "backups", "db")
	for _, v := range []string{"pre-0.1.0", "pre-0.2.0"} {
		if err := os.MkdirAll(filepath.Join(backups, v), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := listNames(t, backups); got != "pre-0.1.0,pre-0.2.0" {
		t.Fatalf("backups = %q, want the two old ones untouched and no pre-1.1.0", got)
	}
}

// A backup directory that exists before the run is not the Migrator's
// to remove when Backup reports unsupported.
func TestUnsupportedBackupKeepsAnExistingDirectory(t *testing.T) {
	d := &unsupportedDriver{version: "1.0.0", backupUnsup: true}
	m, dir := unsupportedMigrator(t, d)
	old := filepath.Join(dir, "backups", "db", "pre-1.1.0")
	if err := os.MkdirAll(old, 0o750); err != nil {
		t.Fatal(err)
	}
	_ = m.Run(context.Background())
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("existing backup dir removed: %v", err)
	}
}

func TestRollbackLatestWithoutBackupMatchesSentinels(t *testing.T) {
	d := &unsupportedDriver{version: "1.0.0", backupUnsup: true}
	m, _ := unsupportedMigrator(t, d, WithManualRollback())
	_ = m.Run(context.Background())
	err := m.RollbackLatest()
	if !errors.Is(err, ErrBackupUnsupported) || !errors.Is(err, ErrNotRestored) {
		t.Fatalf("err = %v, want ErrBackupUnsupported and ErrNotRestored", err)
	}
	if d.version != "1.2.0" {
		t.Fatalf("version = %q, want 1.2.0 (unchanged)", d.version)
	}
}

func TestRollbackLatestUnsupportedRestore(t *testing.T) {
	d := &unsupportedDriver{version: "1.0.0", restoreUnsup: true}
	m, _ := unsupportedMigrator(t, d, WithManualRollback())
	_ = m.Run(context.Background())
	err := m.RollbackLatest()
	if !errors.Is(err, ErrBackupUnsupported) || !errors.Is(err, ErrNotRestored) {
		t.Fatalf("err = %v, want ErrBackupUnsupported and ErrNotRestored", err)
	}
	if d.version != "1.2.0" {
		t.Fatalf("version = %q, want 1.2.0 (unchanged)", d.version)
	}
}

func TestNotRestoredStillUnlocks(t *testing.T) {
	d := &unsupportedDriver{version: "1.0.0", backupUnsup: true}
	m, _ := unsupportedMigrator(t, lockedUnsupported{d})
	if err := m.Run(context.Background()); !errors.Is(err, ErrNotRestored) {
		t.Fatalf("err = %v", err)
	}
	if d.unlockCalls != 1 {
		t.Fatalf("unlock called %d times, want 1", d.unlockCalls)
	}
}
