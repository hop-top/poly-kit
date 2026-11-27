package postgres

import (
	"errors"
	"strings"
	"testing"

	"hop.top/kit/go/core/upgrade"
)

func TestValidateTable(t *testing.T) {
	good := []string{"versions", "_v", "Schema_Versions1", "a"}
	for _, n := range good {
		if err := validateTable(n); err != nil {
			t.Errorf("validateTable(%q) = %v, want nil", n, err)
		}
	}
	bad := []string{"", "1abc", "schema-versions", "a b", "a;drop", `"q"`, "s.t",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	for _, n := range bad {
		if err := validateTable(n); err == nil {
			t.Errorf("validateTable(%q) = nil, want error", n)
		}
	}
}

func TestNewRejectsBadInput(t *testing.T) {
	if _, err := New(nil, "versions"); err == nil {
		t.Error("New(nil db) = nil error")
	}
	if _, err := New(nil, "bad-name"); err == nil || !strings.Contains(err.Error(), "invalid table name") {
		t.Errorf("New(nil, bad name) = %v, want the table-name error first", err)
	}
}

func TestOptions(t *testing.T) {
	d := &Driver{name: "postgres"}
	if d.Name() != "postgres" {
		t.Fatalf("default name = %q", d.Name())
	}
	WithName("custom")(d)
	if d.Name() != "custom" {
		t.Fatalf("WithName: got %q", d.Name())
	}
}

func TestLockKeyText(t *testing.T) {
	if got := lockKey("t", "app"); got != "kit-upgrade:t:app" {
		t.Fatalf("lockKey = %q", got)
	}
	if got := ddlKey("t"); got != "kit-upgrade:t:ddl" {
		t.Fatalf("ddlKey = %q", got)
	}
}

func TestBackupRestoreUnsupported(t *testing.T) {
	d := &Driver{name: "postgres"}
	if err := d.Backup(t.TempDir()); !errors.Is(err, upgrade.ErrBackupUnsupported) {
		t.Fatalf("backup = %v", err)
	}
	if err := d.Restore(t.TempDir()); !errors.Is(err, upgrade.ErrBackupUnsupported) {
		t.Fatalf("restore = %v", err)
	}
	if !d.StoresVersion() {
		t.Fatal("StoresVersion = false")
	}
}
