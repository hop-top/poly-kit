// Package sqlite provides a SchemaDriver for SQLite database files.
package sqlite

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"hop.top/kit/go/core/upgrade"
)

// Driver implements upgrade.SchemaDriver for a SQLite database file.
type Driver struct {
	name   string
	dbPath string
	tool   string
}

// New creates a SQLite schema driver.
// The name defaults to "sqlite" and tool to "" if not set via options.
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

// WithTool sets the tool name for version file resolution.
func WithTool(tool string) Option {
	return func(d *Driver) { d.tool = tool }
}

func (d *Driver) Name() string { return d.name }

func (d *Driver) Version() (string, error) {
	return upgrade.ReadVersionFile(d.tool, d.name)
}

func (d *Driver) SetVersion(version string) error {
	// Version file is written by the migrator; driver is a no-op.
	return nil
}

// Backup copies the database file to the dest directory.
func (d *Driver) Backup(dest string) error {
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	src, err := os.Open(d.dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to back up
		}
		return fmt.Errorf("open db: %w", err)
	}
	defer src.Close()

	dstPath := filepath.Join(dest, filepath.Base(d.dbPath))
	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy db: %w", err)
	}
	return dst.Close()
}

// Restore copies the backup database over the current one.
func (d *Driver) Restore(src string) error {
	backupPath := filepath.Join(src, filepath.Base(d.dbPath))
	in, err := os.Open(backupPath)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer in.Close()

	out, err := os.Create(d.dbPath)
	if err != nil {
		return fmt.Errorf("create db: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("restore db: %w", err)
	}
	return out.Close()
}

var _ upgrade.SchemaDriver = (*Driver)(nil)
