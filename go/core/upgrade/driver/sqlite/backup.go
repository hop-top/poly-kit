package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"hop.top/kit/go/core/upgrade"

	_ "modernc.org/sqlite" // registers the "sqlite" driver Backup opens
)

// sidecars are the files SQLite keeps beside a database. A -wal or hot
// -journal left from the replaced database would be replayed onto the
// restored one the next time it is opened; -shm indexes that WAL.
var sidecars = []string{"-wal", "-shm", "-journal"}

// busyTimeout is how long the driver's own connection waits on a lock.
const busyTimeout = 5 * time.Second

// backupName is the backup file's name inside the backup directory: the
// database file's base name, or <schema>.db when there is no path.
func (d *Driver) backupName() string {
	if d.dbPath != "" {
		return filepath.Base(d.dbPath)
	}
	return d.name + ".db"
}

// Backup writes a consistent snapshot of the database to
// <dest>/<database file name> with VACUUM INTO, which reads through the
// WAL: rows committed but not yet checkpointed are in the snapshot.
//
// Path only: a missing database file is "nothing to back up" (nil, no
// file); otherwise Backup opens a connection of its own and closes it.
// With WithDB the snapshot runs on the caller's handle, left open. Both
// need no open transaction on the connection that runs it.
func (d *Driver) Backup(dest string) error {
	if d.db == nil {
		if _, err := os.Stat(d.dbPath); errors.Is(err, fs.ErrNotExist) {
			return nil // nothing to back up
		} else if err != nil {
			return fmt.Errorf("stat db: %w", err)
		}
	}
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	target := filepath.Join(dest, d.backupName())
	tmp := target + ".tmp"
	// VACUUM INTO refuses an existing file; a leftover is stale.
	for _, p := range []string{tmp, target} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("clear backup file: %w", err)
		}
	}
	if err := d.vacuumInto(context.Background(), tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup db: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup db: %w", err)
	}
	return nil
}

// vacuumInto snapshots the database to file. The file name is a bound
// parameter, so no path needs quoting.
func (d *Driver) vacuumInto(ctx context.Context, file string) error {
	db := d.db
	if db == nil {
		own, err := sql.Open("sqlite", fileDSN(d.dbPath))
		if err != nil {
			return err
		}
		defer own.Close()
		db = own
	}
	_, err := db.ExecContext(ctx, "VACUUM INTO ?", file)
	return err
}

// fileDSN is a read-write, never-create URI for path, with the path
// escaped so spaces, '?', '#' and '%' stay part of the file name.
func fileDSN(path string) string {
	esc := (&url.URL{Path: path}).EscapedPath()
	return fmt.Sprintf("file:%s?mode=rw&_pragma=busy_timeout(%d)",
		esc, busyTimeout.Milliseconds())
}

// Restore replaces the database file with the backup in src, then
// removes the replaced database's -wal, -shm and -journal files.
//
// Precondition: no open handle on the file, in this process or another.
// A connection left open keeps using the replaced file and may recreate
// its -wal beside the restored one.
//
// With WithDB it restores nothing and returns an error matching
// upgrade.ErrBackupUnsupported: the file cannot be swapped under the
// caller's live pool. The backup file stays for manual recovery.
//
// The backup is copied to a temporary file beside the database, synced
// and renamed over it, so a failure before the rename leaves the
// database and its sidecars untouched. An error after the rename means
// the database already holds the backup; a sidecar named in the error
// must be removed before the database is opened.
func (d *Driver) Restore(src string) error {
	if d.db != nil {
		return fmt.Errorf("restore %s under a caller-owned handle: %w",
			d.dbPath, upgrade.ErrBackupUnsupported)
	}
	in, err := os.Open(filepath.Join(src, d.backupName()))
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer in.Close()

	dir := filepath.Dir(d.dbPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create db dir: %w", err)
	}
	mode := os.FileMode(0o600)
	if st, err := os.Stat(d.dbPath); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(d.dbPath)+".restore-*")
	if err != nil {
		return fmt.Errorf("create db: %w", err)
	}
	if err := fillTemp(tmp, in, mode); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("restore db: %w", err)
	}
	if err := os.Rename(tmp.Name(), d.dbPath); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("restore db: %w", err)
	}

	var errs []error
	for _, suffix := range sidecars {
		p := d.dbPath + suffix
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove stale %s: %w", p, err))
		}
	}
	if runtime.GOOS != "windows" {
		if err := syncDir(dir); err != nil {
			errs = append(errs, fmt.Errorf("fsync dir %s: %w", dir, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("restore: %s replaced: %w", d.dbPath, err)
	}
	return nil
}

// fillTemp copies in to tmp, sets mode, syncs and closes tmp.
func fillTemp(tmp *os.File, in io.Reader, mode os.FileMode) error {
	_, err := io.Copy(tmp, in)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	return err
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
