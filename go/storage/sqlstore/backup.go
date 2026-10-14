package sqlstore

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

	"hop.top/kit/go/storage/blob"

	_ "modernc.org/sqlite"
)

// ErrRetention marks a failure to delete old backups after a new one was
// written. The new backup is complete and its path is returned alongside
// the error, so a caller may treat errors.Is(err, ErrRetention) as safe
// to proceed past.
var ErrRetention = errors.New("sqlstore: backup retention")

// snapshotBusyTimeoutMS is the busy timeout of the connection
// BackupBeforeMigrate opens itself (no WithDB). It only matters for a
// database in rollback-journal mode, where a writer can briefly hold a
// lock that keeps a reader out; in WAL mode readers never wait.
const snapshotBusyTimeoutMS = 5000

// maxNameAttempts bounds the search for a backup name no file holds yet.
const maxNameAttempts = 1000

// Option configures BackupBeforeMigrate.
type Option func(*backupConfig)

// backupConfig holds resolved backup options.
type backupConfig struct {
	dir    string
	naming Naming
	keep   int
	db     *sql.DB

	// now and remove are test seams.
	now    func() time.Time
	remove func(string) error
}

// WithBackupDir overrides the directory where the backup file is written.
// When unset, BackupBeforeMigrate writes to <dbDir>/.dbs/ (a hidden sibling
// directory of the source database) so backups do not clutter the data dir.
// The directory is created with mode 0700 if it does not exist; an
// existing directory keeps its mode.
func WithBackupDir(dir string) Option {
	return func(c *backupConfig) { c.dir = dir }
}

// WithNaming replaces the default backup file naming. The Naming also
// decides which files in the backup directory WithKeep may delete.
func WithNaming(n Naming) Option {
	return func(c *backupConfig) { c.naming = n }
}

// WithKeep keeps only the newest n backups of the database in the backup
// directory: after a new backup is safely on disk, older ones the Naming
// recognizes are deleted. n <= 0 (the default) keeps every backup.
func WithKeep(n int) Option {
	return func(c *backupConfig) { c.keep = n }
}

// WithDB takes the snapshot through db instead of a connection
// BackupBeforeMigrate opens itself. Pass the handle the application
// already holds on dbPath whenever it was opened with a SQLite library
// other than modernc.org/sqlite (mattn/go-sqlite3, for instance): two
// SQLite copies in one process do not share POSIX lock state, so opening
// the same file through both can corrupt it. db must be open on dbPath.
func WithDB(db *sql.DB) Option {
	return func(c *backupConfig) { c.db = db }
}

// BackupBeforeMigrate is BackupBeforeMigrateContext with
// context.Background().
func BackupBeforeMigrate(dbPath string, version int, opts ...Option) (string, error) {
	return BackupBeforeMigrateContext(context.Background(), dbPath, version, opts...)
}

// BackupBeforeMigrateContext writes a consistent copy of the SQLite
// database at dbPath to a timestamped backup, for use right before a
// schema migration. The default backup name is:
//
//	<basename>.pre-v<version>.<20060102-150405>.bak
//
// with the timestamp in UTC; WithNaming replaces it. A name some file
// already holds is never overwritten: the Naming is asked again with the
// next attempt number.
//
// The copy is taken with SQLite's VACUUM INTO, which reads the database
// in one transaction: it includes rows still in the WAL and is
// consistent while other connections write. It is written owner-only
// (0600) under a temporary name in the backup directory, fsynced,
// renamed into place, and the directory is fsynced, so a crash never
// leaves a partial file under a backup name. The source is opened
// read-only and never checkpointed.
//
// By default the backup is written to <dbDir>/.dbs/. Pass WithBackupDir to
// override. The destination directory is created (mode 0700) on first
// write.
//
// Returns the backup path on success. If the source file does not exist
// (first run), returns ("", nil): there is nothing to back up. Any other
// failure returns an error and leaves no backup; the caller must not
// migrate without one. A WithKeep pruning failure returns the new
// backup's path and an error wrapping ErrRetention.
func BackupBeforeMigrateContext(ctx context.Context, dbPath string, version int, opts ...Option) (string, error) {
	if _, err := os.Stat(dbPath); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("backup: stat %s: %w", dbPath, err)
	}

	cfg := resolveBackupConfig(opts)
	dir := cfg.dir
	if dir == "" {
		dir = filepath.Join(filepath.Dir(dbPath), ".dbs")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create backup dir %s: %w", dir, err)
	}

	tmp, err := snapshotToTemp(ctx, cfg.db, dbPath, dir)
	if err != nil {
		return "", fmt.Errorf("backup %s: %w", dbPath, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmp)
		}
	}()

	final, err := freeBackupName(dir, cfg.naming, dbPath, version, cfg.now().UTC())
	if err != nil {
		return "", fmt.Errorf("backup %s: %w", dbPath, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", fmt.Errorf("backup %s: rename: %w", dbPath, err)
	}
	committed = true
	if runtime.GOOS != "windows" {
		if err := syncPath(dir); err != nil {
			return final, fmt.Errorf("backup %s: fsync dir %s: %w", dbPath, dir, err)
		}
	}

	if cfg.keep > 0 {
		if err := pruneBackups(dir, cfg, dbPath); err != nil {
			return final, fmt.Errorf("%w: %s: %w", ErrRetention, dir, err)
		}
	}
	return final, nil
}

// Backup writes a consistent copy of the SQLite database at dbPath into
// the given blob.Store under key. If dest is nil, falls back to
// BackupBeforeMigrateContext alongside the DB.
//
// The copy is taken with VACUUM INTO into a temporary owner-only file
// beside the database, streamed to dest, and removed.
func Backup(ctx context.Context, dbPath string, dest blob.Store, key string) error {
	if dest == nil {
		_, err := BackupBeforeMigrateContext(ctx, dbPath, 0)
		return err
	}

	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("backup: open db: %w", err)
	}
	tmp, err := snapshotToTemp(ctx, nil, dbPath, filepath.Dir(dbPath))
	if err != nil {
		return fmt.Errorf("backup: snapshot: %w", err)
	}
	defer os.Remove(tmp)

	f, err := os.Open(tmp)
	if err != nil {
		return fmt.Errorf("backup: open snapshot: %w", err)
	}
	defer f.Close()

	if err := dest.Put(ctx, key, f, "application/x-sqlite3"); err != nil {
		return fmt.Errorf("backup: blob put: %w", err)
	}
	return nil
}

// Restore retrieves a SQLite backup from src blob.Store at key and writes
// it to dbPath, overwriting any existing file. If src is nil, this is a
// no-op (no remote store configured).
func Restore(ctx context.Context, dbPath string, src blob.Store, key string) error {
	if src == nil {
		return nil
	}

	rc, err := src.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("restore: blob get: %w", err)
	}
	defer rc.Close()

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		return fmt.Errorf("restore: mkdir: %w", err)
	}

	tmp := dbPath + ".restore.tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("restore: create tmp: %w", err)
	}

	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("restore: write: %w", err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("restore: sync: %w", err)
	}
	out.Close()

	if err := os.Rename(tmp, dbPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("restore: rename: %w", err)
	}
	return nil
}

func resolveBackupConfig(opts []Option) backupConfig {
	cfg := backupConfig{now: time.Now, remove: os.Remove}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.naming == nil {
		cfg.naming = defaultNaming{}
	}
	return cfg
}

// snapshotToTemp copies the database at dbPath into a new owner-only
// temporary file in dir with VACUUM INTO and fsyncs it. It returns the
// temporary file's path; on error no file is left behind.
func snapshotToTemp(ctx context.Context, db *sql.DB, dbPath, dir string) (string, error) {
	// VACUUM INTO accepts an existing empty file, so CreateTemp both
	// reserves a unique name and gives the copy mode 0600.
	f, err := os.CreateTemp(dir, "."+filepath.Base(dbPath)+".*.partial")
	if err != nil {
		return "", fmt.Errorf("create temp: %w", err)
	}
	tmp := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("create temp: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()

	if db == nil {
		uri, err := readOnlyURI(dbPath)
		if err != nil {
			return "", err
		}
		db, err = sql.Open("sqlite", uri)
		if err != nil {
			return "", fmt.Errorf("open source: %w", err)
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		return "", fmt.Errorf("vacuum into: %w", err)
	}
	if err := syncPath(tmp); err != nil {
		return "", fmt.Errorf("fsync: %w", err)
	}
	ok = true
	return tmp, nil
}

// readOnlyURI returns a modernc.org/sqlite DSN that opens path read-only
// (never creating it) with a busy timeout. The path is escaped so names
// holding '?', '#' or '%' address the right file.
func readOnlyURI(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	p := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" {
		p = "/" + p
	}
	u := url.URL{
		Scheme:   "file",
		OmitHost: true,
		Path:     p,
		RawQuery: fmt.Sprintf("mode=ro&_pragma=busy_timeout(%d)", snapshotBusyTimeoutMS),
	}
	return u.String(), nil
}

// syncPath fsyncs the file or directory at p.
func syncPath(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	serr := f.Sync()
	cerr := f.Close()
	if serr != nil {
		return serr
	}
	return cerr
}
