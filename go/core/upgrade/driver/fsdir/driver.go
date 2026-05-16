// Package fsdir provides a SchemaDriver for filesystem directories.
package fsdir

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hop.top/kit/go/core/upgrade"
)

// Driver implements upgrade.SchemaDriver for a filesystem directory.
type Driver struct {
	name    string
	tool    string
	dirPath string
}

// New creates a filesystem directory schema driver.
func New(name, dirPath string) *Driver {
	return &Driver{
		name:    name,
		dirPath: dirPath,
	}
}

// Option configures a fsdir driver.
type Option func(*Driver)

// WithTool sets the tool name for version file resolution.
func WithTool(tool string) Option {
	return func(d *Driver) { d.tool = tool }
}

// NewWithOptions creates a fsdir driver with options.
func NewWithOptions(name, dirPath string, opts ...Option) *Driver {
	d := New(name, dirPath)
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

// Backup creates a tar.gz archive of the directory in dest.
func (d *Driver) Backup(dest string) error {
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	if _, err := os.Stat(d.dirPath); os.IsNotExist(err) {
		return nil // nothing to back up
	}

	archivePath := filepath.Join(dest, d.name+".tar.gz")
	f, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	return filepath.Walk(d.dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(d.dirPath, path)
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = rel

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(tw, file)
		return err
	})
}

// Restore removes the current directory and extracts the archive.
func (d *Driver) Restore(src string) error {
	archivePath := filepath.Join(src, d.name+".tar.gz")
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		return fmt.Errorf("backup archive not found: %s", archivePath)
	}

	// Remove existing directory.
	if err := os.RemoveAll(d.dirPath); err != nil {
		return fmt.Errorf("remove dir: %w", err)
	}
	if err := os.MkdirAll(d.dirPath, 0o750); err != nil {
		return fmt.Errorf("recreate dir: %w", err)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		target := filepath.Join(d.dirPath, header.Name)
		// Guard against path traversal using filepath.Rel (immune to
		// prefix-matching bypasses like "dir" vs "dir2").
		rel, relErr := filepath.Rel(d.dirPath, filepath.Clean(target))
		if relErr != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("path traversal blocked: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			out, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil { //nolint:gosec
				out.Close()
				return err
			}
			out.Close()
		}
	}
	return nil
}

var _ upgrade.SchemaDriver = (*Driver)(nil)
