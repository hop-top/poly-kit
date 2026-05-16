package xdg

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// backupSubdir is the conventional subdirectory under a file's parent
// where backups are written, mirroring the auto-memory note "DB
// backups in .tlc/.dbs/" so backups never sit next to the live file.
const backupSubdir = ".dbs"

// BackupBeforeMigrate copies srcPath to a timestamped backup under
// <dirOf(srcPath)>/.dbs/<RFC3339>.<basename>, then invokes migrator
// against srcPath. On migrator success the backup path is returned
// unchanged; on migrator error the live file is restored from the
// backup before the error is returned.
//
// Use this for non-DB migrations (state files, JSON snapshots,
// metadata blobs) where a generic file copy is the right primitive.
// Use sqlstore.BackupBeforeMigrate for SQLite databases — that
// helper checkpoints the WAL before copying, which this generic
// helper deliberately does not do.
//
// If srcPath does not exist, BackupBeforeMigrate returns ("", nil)
// and migrator is not called — there is nothing to back up.
func BackupBeforeMigrate(srcPath string, migrator func(string) error) (string, error) {
	info, err := os.Stat(srcPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("xdg: stat %s: %w", srcPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("xdg: backup source %s is not a regular file", srcPath)
	}

	dir := filepath.Dir(srcPath)
	backupDir := filepath.Join(dir, backupSubdir)
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		return "", fmt.Errorf("xdg: mkdir %s: %w", backupDir, err)
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	backupPath := filepath.Join(backupDir, ts+"."+filepath.Base(srcPath))

	if err := copyFile(srcPath, backupPath); err != nil {
		return "", fmt.Errorf("xdg: backup %s: %w", srcPath, err)
	}

	if migrator == nil {
		return backupPath, nil
	}

	if err := migrator(srcPath); err != nil {
		// Restore from backup, best-effort. Surface the migrator error
		// either way so callers don't silently see a half-applied state.
		if rerr := copyFile(backupPath, srcPath); rerr != nil {
			return backupPath, fmt.Errorf(
				"xdg: migrate failed (%w); restore from %s also failed: %w",
				err, backupPath, rerr)
		}
		return backupPath, fmt.Errorf("xdg: migrate failed, restored from %s: %w",
			backupPath, err)
	}

	return backupPath, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// Preserve mode bits from the source so the migrated file keeps
	// the same permissions as the original.
	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
