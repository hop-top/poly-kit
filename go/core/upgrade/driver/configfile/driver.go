// Package configfile provides a SchemaDriver for configuration files.
package configfile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hop.top/kit/go/core/upgrade"
)

// Driver implements upgrade.SchemaDriver for one or more config files.
type Driver struct {
	name  string
	tool  string
	paths []string
}

// New creates a config file schema driver that manages the given file paths.
func New(paths ...string) *Driver {
	return &Driver{
		name:  "config",
		paths: paths,
	}
}

// Option configures a config file driver.
type Option func(*Driver)

// WithName overrides the schema name (default "config").
func WithName(name string) Option {
	return func(d *Driver) { d.name = name }
}

// WithTool sets the tool name for version file resolution.
func WithTool(tool string) Option {
	return func(d *Driver) { d.tool = tool }
}

// NewWithOptions creates a config file driver with options.
func NewWithOptions(opts []Option, paths ...string) *Driver {
	d := New(paths...)
	for _, o := range opts {
		o(d)
	}
	return d
}

func (d *Driver) Name() string { return d.name }

func (d *Driver) Version() (string, error) {
	return upgrade.ReadVersionFile(d.tool, d.name)
}

func (d *Driver) SetVersion(version string) error {
	return nil
}

// Backup copies all managed files to the dest directory.
func (d *Driver) Backup(dest string) error {
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	for _, p := range d.paths {
		backupName := backupFileName(p)
		if err := copyFile(p, filepath.Join(dest, backupName)); err != nil {
			if os.IsNotExist(err) {
				continue // skip missing files
			}
			return fmt.Errorf("backup %s: %w", backupName, err)
		}
	}
	return nil
}

// Restore copies backed-up files back to their original locations.
func (d *Driver) Restore(src string) error {
	for _, p := range d.paths {
		backupFile := filepath.Join(src, backupFileName(p))
		if _, err := os.Stat(backupFile); os.IsNotExist(err) {
			continue
		}
		if err := copyFile(backupFile, p); err != nil {
			return fmt.Errorf("restore %s: %w", backupFileName(p), err)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// backupFileName returns a collision-safe filename derived from the full path.
// e.g. "/etc/foo/config.yaml" → "_etc_foo_config.yaml"
func backupFileName(p string) string {
	return strings.ReplaceAll(filepath.Clean(p), string(os.PathSeparator), "_")
}

var _ upgrade.SchemaDriver = (*Driver)(nil)
