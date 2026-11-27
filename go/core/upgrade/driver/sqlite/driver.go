// Package sqlite provides a SchemaDriver for SQLite database files.
//
// Two modes. Path only: New(dbPath) opens its own short-lived connection
// for Backup and swaps the file on Restore. Handle: New(dbPath,
// WithDB(db)) backs up through the caller's *sql.DB, never closes it, and
// cannot restore (the file cannot be replaced under a live pool).
package sqlite

import (
	"database/sql"
	"fmt"
	"path/filepath"

	"hop.top/kit/go/core/upgrade"
	"hop.top/kit/go/core/xdg"
)

// Driver implements upgrade.SchemaDriver and upgrade.Locker for a SQLite
// database file.
type Driver struct {
	name    string
	dbPath  string
	tool    string
	dataDir string
	db      *sql.DB
}

// New creates a SQLite schema driver for the database file at dbPath.
// The name defaults to "sqlite" and tool to "" if not set via options.
// With WithDB, dbPath only names the backup file (and may be empty).
func New(dbPath string, opts ...Option) *Driver {
	d := &Driver{
		name:   "sqlite",
		dbPath: dbPath,
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Option configures a SQLite driver.
type Option func(*Driver)

// WithName overrides the schema name (default "sqlite").
func WithName(name string) Option {
	return func(d *Driver) { d.name = name }
}

// WithDataDir reads the version file under dir, for a Migrator built
// with upgrade.WithDataDir(dir).
func WithDataDir(dir string) Option {
	return func(d *Driver) { d.dataDir = dir }
}

// WithTool sets the tool name for version file resolution.
func WithTool(tool string) Option {
	return func(d *Driver) { d.tool = tool }
}

// WithDB runs Backup on the caller's handle instead of a connection of
// the driver's own. The driver never closes db. Restore then returns
// upgrade.ErrBackupUnsupported: the file cannot be swapped under a live
// pool, and the backup file stays for manual recovery.
func WithDB(db *sql.DB) Option {
	return func(d *Driver) { d.db = db }
}

func (d *Driver) Name() string { return d.name }

func (d *Driver) Version() (string, error) {
	if d.dataDir != "" {
		return upgrade.ReadVersionFileIn(d.dataDir, d.name)
	}
	return upgrade.ReadVersionFile(d.tool, d.name)
}

func (d *Driver) SetVersion(version string) error {
	// Version file is written by the migrator; driver is a no-op.
	return nil
}

// stateDir is the directory the driver's version file lives in:
// <data dir>/<name>, the data dir being WithDataDir's or
// xdg.DataDir("hop/<tool>"). The lock file sits here.
func (d *Driver) stateDir() (string, error) {
	if d.dataDir != "" {
		return filepath.Join(d.dataDir, d.name), nil
	}
	base, err := xdg.DataDir(filepath.Join("hop", d.tool))
	if err != nil {
		return "", fmt.Errorf("resolve data dir: %w", err)
	}
	return filepath.Join(base, d.name), nil
}

var (
	_ upgrade.SchemaDriver = (*Driver)(nil)
	_ upgrade.Locker       = (*Driver)(nil)
)
