package upgrade

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/runtime/domain"
)

// ErrVerify marks a failed Migration.Verify or WithVerify check:
// errors.Is(err, ErrVerify). A canceled run wraps the context's error
// instead (errors.Is(err, context.Canceled)).
var ErrVerify = errors.New("upgrade: verify failed")

// verifyError keeps the verify step's own text while matching ErrVerify.
type verifyError struct{ err error }

func (e verifyError) Error() string        { return e.err.Error() }
func (e verifyError) Unwrap() error        { return e.err }
func (e verifyError) Is(target error) bool { return target == ErrVerify }

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

// WithRegistry makes the Migrator read migrations from r instead of
// DefaultRegistry (what RegisterMigration fills).
func WithRegistry(r *Registry) MigratorOption {
	return func(m *Migrator) { m.registry = r }
}

// WithDataDir keeps version files and backups under dir instead of
// xdg.DataDir("hop/<tool>"). Drivers that read the version file need the
// same dir (ReadVersionFileIn, or the drivers' WithDataDir).
func WithDataDir(dir string) MigratorOption {
	return func(m *Migrator) { m.dataDir = dir }
}

// WithVerify checks each schema's final state after its last pending
// migration and before the version is recorded. A failure (ErrVerify)
// rolls back every migration applied to that schema in the run.
func WithVerify(fn func(ctx context.Context, schema, version string) error) MigratorOption {
	return func(m *Migrator) { m.verify = fn }
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
	registry     *Registry
	dataDir      string
	verify       func(ctx context.Context, schema, version string) error
	pub          domain.EventPublisher
	topics       MigrationTopics
}

// NewMigrator creates a Migrator for the given tool and target version.
func NewMigrator(tool, version string, opts ...MigratorOption) *Migrator {
	m := &Migrator{
		tool:         tool,
		version:      strings.TrimPrefix(version, "v"),
		autoRollback: true,
		retention:    defaultRetention,
		registry:     defaultRegistry,
		topics:       DefaultMigrationTopics,
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
// apply Up (then Verify) in order, run the final verify, record the
// version. On failure: rollback (Down in reverse, then Restore). A
// context canceled between steps stops before the next Up.
func (m *Migrator) Run(ctx context.Context) error {
	start := time.Now()
	quiet := context.WithoutCancel(ctx)
	m.emit(quiet, m.topics.RunStarted, MigrationRunPayload{Tool: m.tool, To: m.version})
	applied := 0
	for _, d := range m.drivers {
		n, err := m.runSchema(ctx, d)
		applied += n
		if err != nil {
			err = fmt.Errorf("migrate %s: %w", d.Name(), err)
			m.emit(quiet, m.topics.RunFailed, MigrationRunPayload{Tool: m.tool, To: m.version,
				Schema: d.Name(), Error: err.Error(), DurationMS: millis(start)})
			return err
		}
	}
	m.emit(quiet, m.topics.RunCompleted, MigrationRunPayload{Tool: m.tool, To: m.version,
		Applied: &applied, DurationMS: millis(start)})
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
		pending := m.registry.pending(d.Name(), stored, m.version)
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

func (m *Migrator) backups(schema string) *backupOrchestrator {
	o := newBackupOrchestrator(m.tool, schema, m.retention)
	o.base = m.dataDir
	return o
}

// RollbackLatest restores the latest backup for each driver.
// Only works when auto-rollback is disabled (manual mode).
func (m *Migrator) RollbackLatest() error {
	if m.autoRollback {
		return fmt.Errorf("rollback: only available in manual rollback mode")
	}
	for _, d := range m.drivers {
		latest, err := m.backups(d.Name()).latestBackup()
		if err != nil {
			return fmt.Errorf("rollback %s: %w", d.Name(), err)
		}
		if err := d.Restore(latest); err != nil {
			return fmt.Errorf("rollback %s: %w", d.Name(), err)
		}
	}
	return nil
}

// stepFailure is what stopped a schema's run, with what to roll back.
type stepFailure struct {
	stage   string // up, verify, final, canceled
	version string // the version being attempted
	from    string
	err     error
	undo    []Migration
}

func (f stepFailure) wrap() error {
	switch f.stage {
	case "verify":
		return fmt.Errorf("verify %s failed: %w", f.version, verifyError{f.err})
	case "final":
		return fmt.Errorf("final verify %s failed: %w", f.version, verifyError{f.err})
	case "canceled":
		return fmt.Errorf("up %s canceled: %w", f.version, f.err)
	}
	return fmt.Errorf("up %s failed: %w", f.version, f.err)
}

func (m *Migrator) runSchema(ctx context.Context, d SchemaDriver) (int, error) {
	current, err := d.Version()
	if err != nil {
		return 0, fmt.Errorf("read version: %w", err)
	}

	pending := m.registry.pending(d.Name(), current, m.version)
	if len(pending) == 0 {
		return 0, nil
	}

	orch := m.backups(d.Name())

	// Backup before applying migrations.
	backupPath, err := orch.backupDir(pending[0].Version)
	if err != nil {
		return 0, fmt.Errorf("prepare backup: %w", err)
	}
	if err := d.Backup(backupPath); err != nil {
		return 0, fmt.Errorf("backup: %w", err)
	}

	fail, n := m.applyAll(ctx, d.Name(), current, pending)
	if fail != nil {
		return n, m.fail(ctx, d, current, backupPath, *fail)
	}

	// Record new version.
	lastVersion := pending[len(pending)-1].Version
	if err := m.setVersion(d, lastVersion); err != nil {
		return n, fmt.Errorf("set version: %w", err)
	}

	// Prune old backups on success (non-fatal).
	if pruneErr := orch.prune(); pruneErr != nil {
		log.Printf("warning: prune old backups for %s: %v", d.Name(), pruneErr)
	}

	return n, nil
}

// applyAll runs Up and Verify for each pending migration, then the
// final verify. It returns the failure, if any, and how many applied.
func (m *Migrator) applyAll(ctx context.Context, schema, current string, pending []Migration) (*stepFailure, int) {
	quiet := context.WithoutCancel(ctx)
	from := current
	var applied []Migration
	for _, mig := range pending {
		ev := MigrationPayload{Tool: m.tool, Schema: schema, From: from, To: mig.Version}
		if err := ctx.Err(); err != nil {
			return m.failed(quiet, ev, "canceled", err, applied), len(applied)
		}
		m.emit(quiet, m.topics.Started, ev)
		t := time.Now()
		if err := mig.Up(ctx); err != nil {
			return m.failed(quiet, withDuration(ev, t), "up", err, applied), len(applied)
		}
		m.emit(quiet, m.topics.Applied, withDuration(ev, t))
		if mig.Verify != nil {
			t = time.Now()
			if err := mig.Verify(ctx); err != nil {
				// Up completed: the rollback must undo this step too.
				return m.failed(quiet, withDuration(ev, t), "verify", err, append(applied, mig)), len(applied)
			}
			m.emit(quiet, m.topics.Verified, withDuration(ev, t))
		}
		applied = append(applied, mig)
		m.history = append(m.history, AppliedMigration{Version: mig.Version, Schema: mig.Schema})
		from = mig.Version
	}
	if m.verify != nil {
		ev := MigrationPayload{Tool: m.tool, Schema: schema, From: current, To: from}
		t := time.Now()
		if err := m.verify(ctx, schema, from); err != nil {
			return m.failed(quiet, withDuration(ev, t), "final", err, applied), len(applied)
		}
		ev = withDuration(ev, t)
		ev.Final = true
		m.emit(quiet, m.topics.Verified, ev)
	}
	return nil, len(applied)
}

func (m *Migrator) failed(ctx context.Context, ev MigrationPayload, stage string, err error, undo []Migration) *stepFailure {
	ev.Stage, ev.Error = stage, err.Error()
	m.emit(ctx, m.topics.Failed, ev)
	return &stepFailure{stage: stage, version: ev.To, from: ev.From, err: err, undo: undo}
}

func withDuration(ev MigrationPayload, since time.Time) MigrationPayload {
	ev.DurationMS = millis(since)
	return ev
}

// fail rolls back (in auto mode) and builds the schema's error.
func (m *Migrator) fail(ctx context.Context, d SchemaDriver, current, backupPath string, f stepFailure) error {
	err := f.wrap()
	if !m.autoRollback || len(f.undo) == 0 {
		return err
	}
	quiet := context.WithoutCancel(ctx)
	t := time.Now()
	method, rollbackErr := m.rollback(quiet, d, f.undo, backupPath)
	if rollbackErr != nil {
		return fmt.Errorf("%w (rollback also failed: %v)", err, rollbackErr)
	}
	m.emit(quiet, m.topics.Reverted, MigrationPayload{Tool: m.tool, Schema: d.Name(),
		From: f.version, To: current, Method: method, DurationMS: millis(t)})
	return err
}

// rollback runs Down in reverse order; falls back to Restore if a Down
// is missing or fails. It reports which of the two undid the changes.
func (m *Migrator) rollback(
	ctx context.Context,
	d SchemaDriver,
	applied []Migration,
	backupPath string,
) (string, error) {
	for i := len(applied) - 1; i >= 0; i-- {
		mig := applied[i]
		if mig.Down == nil || mig.Down(ctx) != nil {
			return "restore", d.Restore(backupPath)
		}
	}
	return "down", nil
}

// setVersion writes the version file and also calls driver.SetVersion.
//
// The Migrator owns the canonical version via ReadVersionFile/writeVersionFile
// (XDG-based, or WithDataDir). driver.SetVersion is called as a notification
// so that drivers can update internal state (e.g. in-memory cache); the two
// calls are not redundant — they serve different responsibilities.
func (m *Migrator) setVersion(d SchemaDriver, version string) error {
	base, err := m.resolvedDataDir()
	if err != nil {
		return err
	}
	versionDir := filepath.Join(base, d.Name())
	if err := os.MkdirAll(versionDir, 0o750); err != nil {
		return fmt.Errorf("create version dir: %w", err)
	}
	vf := filepath.Join(versionDir, "version")
	if err := os.WriteFile(vf, []byte(version), 0o600); err != nil {
		return fmt.Errorf("write version file: %w", err)
	}

	return d.SetVersion(version)
}

func (m *Migrator) resolvedDataDir() (string, error) {
	if m.dataDir != "" {
		return m.dataDir, nil
	}
	base, err := xdg.DataDir(filepath.Join("hop", m.tool))
	if err != nil {
		return "", fmt.Errorf("resolve data dir: %w", err)
	}
	return base, nil
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
	return readVersionFile(dir)
}

// ReadVersionFileIn reads <dataDir>/<driverName>/version, for a Migrator
// built WithDataDir(dataDir). Returns "" if the file does not exist.
func ReadVersionFileIn(dataDir, driverName string) (string, error) {
	return readVersionFile(filepath.Join(dataDir, driverName))
}

func readVersionFile(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "version"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
