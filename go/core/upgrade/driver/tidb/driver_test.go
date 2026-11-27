package tidb

import (
	"context"
	"errors"
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

// The no-op pair it replaces reported a restore that never happened as
// a successful revert; the old assertions pinned that defect.
func TestDriver_BackupRestoreUnsupported(t *testing.T) {
	d := &Driver{name: "tidb"}
	if err := d.Backup(t.TempDir()); !errors.Is(err, upgrade.ErrBackupUnsupported) {
		t.Fatalf("backup = %v, want ErrBackupUnsupported", err)
	}
	if err := d.Restore(t.TempDir()); !errors.Is(err, upgrade.ErrBackupUnsupported) {
		t.Fatalf("restore = %v, want ErrBackupUnsupported", err)
	}
}

// memVersion keeps the version in memory, so the migrator can run a
// zero Driver whose Backup and Restore are the real ones.
type memVersion struct {
	*Driver
	version string
}

func (m *memVersion) Version() (string, error)  { return m.version, nil }
func (m *memVersion) SetVersion(v string) error { m.version = v; return nil }

// A migration without a Down must not be reported as restored: the
// driver's Restore does nothing, so the run fails with ErrNotRestored
// and the version stays at the step still applied.
func TestDriver_RunWithoutDownIsNotRestored(t *testing.T) {
	reg := upgrade.NewRegistry()
	ok := func(context.Context) error { return nil }
	reg.Register(upgrade.Migration{Version: "1.1.0", Schema: "tidb", Up: ok})
	reg.Register(upgrade.Migration{Version: "1.2.0", Schema: "tidb",
		Up: func(context.Context) error { return errors.New("boom") }})

	var topics []string
	pub := publisherFunc(func(_ context.Context, topic, _ string, _ any) error {
		topics = append(topics, topic)
		return nil
	})
	d := &memVersion{Driver: &Driver{name: "tidb"}, version: "1.0.0"}
	m := upgrade.NewMigrator("tidbtest", "1.2.0", upgrade.WithRegistry(reg),
		upgrade.WithDataDir(t.TempDir()), upgrade.WithEventPublisher(pub))
	m.AddDriver(d)

	err := m.Run(context.Background())
	if !errors.Is(err, upgrade.ErrNotRestored) {
		t.Fatalf("err = %v, want ErrNotRestored", err)
	}
	if !errors.Is(err, upgrade.ErrBackupUnsupported) {
		t.Fatalf("err = %v, want ErrBackupUnsupported as the cause", err)
	}
	if d.version != "1.1.0" {
		t.Fatalf("version = %q, want 1.1.0 (the step still applied)", d.version)
	}
	for _, topic := range topics {
		if topic == string(upgrade.DefaultMigrationTopics.Reverted) {
			t.Fatalf("a reverted event was published: %v", topics)
		}
	}
}

type publisherFunc func(ctx context.Context, topic, source string, payload any) error

func (f publisherFunc) Publish(ctx context.Context, topic, source string, payload any) error {
	return f(ctx, topic, source, payload)
}
