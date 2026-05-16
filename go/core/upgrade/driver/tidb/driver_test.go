package tidb

import (
	"testing"

	"hop.top/kit/go/core/upgrade"
)

// Compile-time interface assertion (runs without build tag).
var _ upgrade.SchemaDriver = (*Driver)(nil)

func TestDriver_Name(t *testing.T) {
	// Cannot instantiate without a real DB; verify name via struct.
	d := &Driver{name: "tidb"}
	if d.Name() != "tidb" {
		t.Fatalf("expected tidb, got %s", d.Name())
	}

	d2 := &Driver{name: "custom"}
	if d2.Name() != "custom" {
		t.Fatalf("expected custom, got %s", d2.Name())
	}
}

func TestDriver_BackupRestoreNoOp(t *testing.T) {
	d := &Driver{name: "tidb"}
	if err := d.Backup("/tmp/noop"); err != nil {
		t.Fatalf("backup should be no-op: %v", err)
	}
	if err := d.Restore("/tmp/noop"); err != nil {
		t.Fatalf("restore should be no-op: %v", err)
	}
}
