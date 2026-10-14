package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"hop.top/kit/go/storage/blob"
)

// sqliteSidecars are the files SQLite keeps beside a database. A -wal or
// hot -journal left from the replaced database would be replayed onto
// the restored one the next time it is opened; -shm indexes that WAL.
var sqliteSidecars = []string{"-wal", "-shm", "-journal"}

// Test seams.
var (
	restoreRename  = os.Rename
	restoreRemove  = os.Remove
	restoreSyncDir = syncPath
)

// Restore retrieves a SQLite backup from src blob.Store at key and
// replaces the database at dbPath with it. If src is nil, this is a
// no-op (no remote store configured).
//
// Every handle on dbPath must be closed first, in this process and any
// other: a connection that stays open keeps reading and writing the
// replaced file, and may recreate its -wal beside the restored one.
//
// The backup is downloaded to a temporary file in dbPath's directory,
// fsynced, and renamed over dbPath. The replaced database's -wal, -shm
// and -journal files are removed after the rename, so SQLite never
// replays them onto the restored file, and the directory is then
// fsynced. The restored file keeps the replaced file's permission bits,
// or is owner-only (0600) when dbPath did not exist.
//
// If the download, write or rename fails, dbPath and its -wal, -shm and
// -journal files are left untouched. An error after the rename (removing
// a stale sidecar, fsyncing the directory) means dbPath already holds
// the backup; a sidecar named in the error must be removed before the
// database is opened.
func Restore(ctx context.Context, dbPath string, src blob.Store, key string) error {
	if src == nil {
		return nil
	}

	rc, err := src.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("restore: blob get: %w", err)
	}
	defer rc.Close()

	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("restore: mkdir: %w", err)
	}

	tmp, err := downloadToTemp(rc, dbPath)
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	if err := restoreRename(tmp, dbPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("restore: rename: %w", err)
	}

	var errs []error
	for _, suffix := range sqliteSidecars {
		p := dbPath + suffix
		if err := restoreRemove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove stale %s: %w", p, err))
		}
	}
	if runtime.GOOS != "windows" {
		if err := restoreSyncDir(dir); err != nil {
			errs = append(errs, fmt.Errorf("fsync dir %s: %w", dir, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("restore: %s replaced: %w", dbPath, err)
	}
	return nil
}

// downloadToTemp copies r into a new temporary file beside dbPath, gives
// it dbPath's permission bits (0600 when dbPath does not exist) and
// fsyncs it. On error no file is left behind.
func downloadToTemp(r io.Reader, dbPath string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(dbPath), "."+filepath.Base(dbPath)+".*.restore")
	if err != nil {
		return "", fmt.Errorf("create temp: %w", err)
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()

	if st, err := os.Stat(dbPath); err == nil {
		if err := f.Chmod(st.Mode().Perm()); err != nil {
			return "", fmt.Errorf("chmod temp: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("stat %s: %w", dbPath, err)
	}
	if _, err := io.Copy(f, r); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close: %w", err)
	}
	ok = true
	return tmp, nil
}
