package upgrade

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"hop.top/kit/go/core/xdg"
)

// MigratorOption configures a Migrator.
type MigratorOption func(*Migrator)

// WithManualRollback disables automatic rollback on migration failure.
func WithManualRollback() MigratorOption {
	return func(m *Migrator) { m.autoRollback = false }
}

// WithBackupRetention sets the number of recent backups to keep per schema.
func WithBackupRetention(n int) MigratorOption {
	return func(m *Migrator) { m.retention = n }
}

// AppliedMigration records a successfully applied migration.
type AppliedMigration struct {
	Version string `json:"version"   table:"VERSION"`
	Schema  string `json:"schema"    table:"SCHEMA"`
}

// SchemaStatus describes the migration state of a single schema.
type SchemaStatus struct {
	Schema  string `json:"schema"  table:"SCHEMA"`
	Current string `json:"current" table:"CURRENT"`
	Target  string `json:"target"  table:"TARGET"`
	Pending int    `json:"pending" table:"PENDING"`
	Err     error  `json:"error,omitempty" table:"-"`
}

// Migrator orchestrates versioned migrations across multiple schema drivers.
type Migrator struct {
	tool         string
	version      string
	drivers      []SchemaDriver
	autoRollback bool
	retention    int
	history      []AppliedMigration
}

// NewMigrator creates a Migrator for the given tool and target version.
func NewMigrator(tool, version string, opts ...MigratorOption) *Migrator {
	m := &Migrator{
		tool:         tool,
		version:      strings.TrimPrefix(version, "v"),
		autoRollback: true,
		retention:    defaultRetention,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// AddDriver registers a schema driver with the migrator.
func (m *Migrator) AddDriver(d SchemaDriver) {
	m.drivers = append(m.drivers, d)
}

// Run executes pending migrations for all registered drivers.
// For each schema: read current version, collect pending, backup,
// apply Up in order. On failure: rollback (Down in reverse, then Restore).
func (m *Migrator) Run(ctx context.Context) error {
	for _, d := range m.drivers {
		if err := m.runSchema(ctx, d); err != nil {
			return fmt.Errorf("migrate %s: %w", d.Name(), err)
		}
	}
	return nil
}

// Tool returns the tool name.
func (m *Migrator) Tool() string { return m.tool }

// Version returns the target version.
func (m *Migrator) Version() string { return m.version }

// Status returns pending migration counts per driver.
func (m *Migrator) Status() []SchemaStatus {
	var out []SchemaStatus
	for _, d := range m.drivers {
		stored, vErr := d.Version()
		pending := pendingMigrations(d.Name(), stored, m.version)
		out = append(out, SchemaStatus{
			Schema:  d.Name(),
			Current: stored,
			Target:  m.version,
			Pending: len(pending),
			Err:     vErr,
		})
	}
	return out
}

// History returns all applied migrations from this session.
func (m *Migrator) History() []AppliedMigration {
	return m.history
}

// RollbackLatest restores the latest backup for each driver.
// Only works when auto-rollback is disabled (manual mode).
func (m *Migrator) RollbackLatest() error {
	if m.autoRollback {
		return fmt.Errorf("rollback: only available in manual rollback mode")
	}
	for _, d := range m.drivers {
		orch := newBackupOrchestrator(m.tool, d.Name(), m.retention)
		latest, err := orch.latestBackup()
		if err != nil {
			return fmt.Errorf("rollback %s: %w", d.Name(), err)
		}
		if err := d.Restore(latest); err != nil {
			return fmt.Errorf("rollback %s: %w", d.Name(), err)
		}
	}
	return nil
}

func (m *Migrator) runSchema(ctx context.Context, d SchemaDriver) error {
	current, err := d.Version()
	if err != nil {
		return fmt.Errorf("read version: %w", err)
	}

	pending := pendingMigrations(d.Name(), current, m.version)
	if len(pending) == 0 {
		return nil
	}

	orch := newBackupOrchestrator(m.tool, d.Name(), m.retention)

	// Backup before applying migrations.
	backupPath, err := orch.backupDir(pending[0].Version)
	if err != nil {
		return fmt.Errorf("prepare backup: %w", err)
	}
	if err := d.Backup(backupPath); err != nil {
		return fmt.Errorf("backup: %w", err)
	}

	// Apply migrations in order.
	var applied []Migration
	for _, mig := range pending {
		if err := mig.Up(ctx); err != nil {
			if m.autoRollback {
				rollbackErr := m.rollback(ctx, d, applied, backupPath)
				if rollbackErr != nil {
					return fmt.Errorf("up %s failed: %w (rollback also failed: %v)",
						mig.Version, err, rollbackErr)
				}
			}
			return fmt.Errorf("up %s failed: %w", mig.Version, err)
		}
		applied = append(applied, mig)
		m.history = append(m.history, AppliedMigration{
			Version: mig.Version,
			Schema:  mig.Schema,
		})
	}

	// Record new version.
	lastVersion := pending[len(pending)-1].Version
	if err := m.setVersion(d, lastVersion); err != nil {
		return fmt.Errorf("set version: %w", err)
	}

	// Prune old backups on success (non-fatal).
	if pruneErr := orch.prune(); pruneErr != nil {
		log.Printf("warning: prune old backups for %s: %v", d.Name(), pruneErr)
	}

	return nil
}

// rollback runs Down in reverse order; falls back to Restore if Down fails.
func (m *Migrator) rollback(
	ctx context.Context,
	d SchemaDriver,
	applied []Migration,
	backupPath string,
) error {
	// Try Down functions in reverse.
	for i := len(applied) - 1; i >= 0; i-- {
		mig := applied[i]
		if mig.Down == nil {
			return d.Restore(backupPath)
		}
		if err := mig.Down(ctx); err != nil {
			return d.Restore(backupPath)
		}
	}
	return nil
}

// setVersion writes the version file and also calls driver.SetVersion.
//
// The Migrator owns the canonical version via ReadVersionFile/writeVersionFile
// (XDG-based). driver.SetVersion is called as a notification so that drivers
// can update internal state (e.g. in-memory cache); the two calls are not
// redundant — they serve different responsibilities.
func (m *Migrator) setVersion(d SchemaDriver, version string) error {
	// Write version file at standard path.
	versionDir, err := versionFileDir(m.tool, d.Name())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(versionDir, 0o750); err != nil {
		return fmt.Errorf("create version dir: %w", err)
	}
	vf := filepath.Join(versionDir, "version")
	if err := os.WriteFile(vf, []byte(version), 0o600); err != nil {
		return fmt.Errorf("write version file: %w", err)
	}

	return d.SetVersion(version)
}

// versionFileDir returns the directory containing the version file.
// Path: xdg.DataDir("hop/<tool>")/<driverName>/
func versionFileDir(tool, driverName string) (string, error) {
	base, err := xdg.DataDir(filepath.Join("hop", tool))
	if err != nil {
		return "", fmt.Errorf("resolve data dir: %w", err)
	}
	return filepath.Join(base, driverName), nil
}

// ReadVersionFile reads the version from the standard version file location.
// Returns "" if the file does not exist.
func ReadVersionFile(tool, driverName string) (string, error) {
	dir, err := versionFileDir(tool, driverName)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, "version"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
