//! Backup and restore helpers for SQLite database files.
//!
//! Mirrors the Go `storage/sqlstore` backup surface:
//!
//! - [`backup_before_migrate`] — timestamped local file copy, written to a
//!   hidden `.dbs/` sibling directory by default.
//! - [`backup_to_blob`] / [`restore_from_blob`] — round-trip through a
//!   [`crate::blob::Store`] (feature `sqlstore-blob`).
//!
//! Every path that reads a live database first issues a
//! `PRAGMA wal_checkpoint(TRUNCATE)` so pages sitting in the write-ahead
//! log are folded back into the main file. Without it a copy of a WAL-mode
//! database can be missing the most recent writes. The checkpoint is
//! best-effort: a file that is not a valid SQLite database yet, or is not
//! in WAL mode, is copied as-is rather than failing the backup.

use std::fs::{self, File};
use std::io;
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

use thiserror::Error;

/// Errors returned by the backup and restore helpers.
#[derive(Debug, Error)]
pub enum BackupError {
    /// A filesystem operation failed.
    #[error("sqlstore/backup: {op}: {source}")]
    Io {
        /// Short name of the failed operation.
        op: &'static str,
        /// Underlying I/O error.
        #[source]
        source: io::Error,
    },

    /// A blob store operation failed.
    #[cfg(feature = "sqlstore-blob")]
    #[error("sqlstore/backup: blob: {0}")]
    Blob(#[from] crate::blob::BlobError),
}

impl BackupError {
    fn io(op: &'static str, source: io::Error) -> Self {
        BackupError::Io { op, source }
    }
}

/// Options for [`backup_before_migrate`].
#[derive(Debug, Clone, Default)]
pub struct BackupOptions {
    /// Directory the backup file is written to.
    ///
    /// When `None`, backups go to `<db dir>/.dbs/`, a hidden sibling of the
    /// source database, so they do not clutter the data directory.
    pub dir: Option<PathBuf>,
}

impl BackupOptions {
    /// Options using the default `.dbs/` destination.
    pub fn new() -> Self {
        Self::default()
    }

    /// Overrides the destination directory.
    #[must_use]
    pub fn with_dir(mut self, dir: impl Into<PathBuf>) -> Self {
        self.dir = Some(dir.into());
        self
    }
}

/// Copies the database at `db_path` to a timestamped backup file.
///
/// The backup is named `<stem>.pre-v<version>.<YYYYmmdd-HHMMSS>.bak` and
/// written to [`BackupOptions::dir`], defaulting to `<db dir>/.dbs/`. The
/// destination directory is created if missing.
///
/// Returns the backup path, or `None` when the source does not exist —
/// a first run has nothing to back up, which is not an error.
///
/// # Errors
///
/// Returns [`BackupError::Io`] when the destination cannot be created or
/// the copy fails.
pub fn backup_before_migrate(
    db_path: impl AsRef<Path>,
    version: i64,
    opts: &BackupOptions,
) -> Result<Option<PathBuf>, BackupError> {
    let db_path = db_path.as_ref();
    if !db_path.exists() {
        return Ok(None);
    }

    wal_checkpoint(db_path);

    let dir = match &opts.dir {
        Some(d) => d.clone(),
        None => db_path
            .parent()
            .unwrap_or_else(|| Path::new("."))
            .join(".dbs"),
    };
    fs::create_dir_all(&dir).map_err(|e| BackupError::io("create backup dir", e))?;

    let stem = db_path
        .file_stem()
        .map(|s| s.to_string_lossy().into_owned())
        .unwrap_or_default();
    let name = format!("{stem}.pre-v{version}.{}.bak", compact_timestamp());
    let backup_path = dir.join(name);

    copy_file(db_path, &backup_path)?;
    Ok(Some(backup_path))
}

/// Dumps the database file at `db_path` into `dest` under `key`.
///
/// # Errors
///
/// Returns [`BackupError::Io`] when the source cannot be read and
/// [`BackupError::Blob`] when the store rejects the write.
#[cfg(feature = "sqlstore-blob")]
pub fn backup_to_blob<S: crate::blob::Store>(
    db_path: impl AsRef<Path>,
    dest: &S,
    key: &str,
) -> Result<(), BackupError> {
    let db_path = db_path.as_ref();
    wal_checkpoint(db_path);

    let f = File::open(db_path).map_err(|e| BackupError::io("open db", e))?;
    dest.put(key, f, "application/x-sqlite3")?;
    Ok(())
}

/// Retrieves a backup from `src` at `key` and writes it to `db_path`,
/// replacing any existing file.
///
/// The download is staged in a sibling temp file, synced, then renamed over
/// the destination. `std::fs::rename` already replaces an existing
/// destination atomically on both Unix (`rename(2)`) and Windows
/// (`MoveFileExW` with `MOVEFILE_REPLACE_EXISTING`, passed unconditionally
/// by the standard library), so no destination-removal step is needed on
/// either platform. An interrupted restore therefore leaves either the
/// previous database or the fully-staged new one on disk — never neither,
/// and never a truncated file — and a concurrent reader never observes a
/// half-written file.
///
/// # Errors
///
/// Returns [`BackupError::Blob`] when the object cannot be fetched and
/// [`BackupError::Io`] when staging, syncing, or renaming fails.
#[cfg(feature = "sqlstore-blob")]
pub fn restore_from_blob<S: crate::blob::Store>(
    db_path: impl AsRef<Path>,
    src: &S,
    key: &str,
) -> Result<(), BackupError> {
    use std::io::Write;

    let db_path = db_path.as_ref();
    let mut reader = src.get(key)?;

    if let Some(parent) = db_path.parent() {
        if !parent.as_os_str().is_empty() {
            fs::create_dir_all(parent).map_err(|e| BackupError::io("mkdir", e))?;
        }
    }

    let tmp = with_suffix(db_path, ".restore.tmp");
    let staged = (|| -> Result<(), BackupError> {
        let mut out = File::create(&tmp).map_err(|e| BackupError::io("create tmp", e))?;
        io::copy(&mut reader, &mut out).map_err(|e| BackupError::io("write", e))?;
        out.flush().map_err(|e| BackupError::io("flush", e))?;
        out.sync_all().map_err(|e| BackupError::io("sync", e))?;
        Ok(())
    })();

    if let Err(err) = staged {
        let _ = fs::remove_file(&tmp);
        return Err(err);
    }

    if let Err(e) = fs::rename(&tmp, db_path) {
        // tmp is still the only copy of the newly-fetched data at this
        // point (rename either succeeds atomically or leaves both files
        // as they were — it never partially applies), and db_path (if it
        // existed) is untouched, so cleaning up the failed tmp is safe.
        let _ = fs::remove_file(&tmp);
        return Err(BackupError::io("rename", e));
    }
    Ok(())
}

/// Folds write-ahead-log pages back into the main database file.
///
/// Best-effort by design: the file may not be a valid SQLite database yet,
/// or may not be in WAL mode. Either way the caller still gets a byte copy
/// of whatever is on disk, matching Go.
fn wal_checkpoint(db_path: &Path) {
    let Ok(conn) = rusqlite::Connection::open(db_path) else {
        return;
    };
    let _ = conn.query_row("PRAGMA wal_checkpoint(TRUNCATE)", [], |_| Ok(()));
}

/// Appends `suffix` to a path's final component.
#[cfg(any(feature = "sqlstore-blob", test))]
fn with_suffix(path: &Path, suffix: &str) -> PathBuf {
    let mut s = path.as_os_str().to_os_string();
    s.push(suffix);
    PathBuf::from(s)
}

fn copy_file(src: &Path, dst: &Path) -> Result<(), BackupError> {
    let mut input = File::open(src).map_err(|e| BackupError::io("open source", e))?;
    let mut output = File::create(dst).map_err(|e| BackupError::io("create backup", e))?;
    io::copy(&mut input, &mut output).map_err(|e| BackupError::io("copy", e))?;
    output.sync_all().map_err(|e| BackupError::io("sync", e))?;
    Ok(())
}

/// Current UTC time as `YYYYmmdd-HHMMSS`, matching Go's
/// `time.Now().UTC().Format("20060102-150405")`.
fn compact_timestamp() -> String {
    let secs = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0);
    let rem = secs.rem_euclid(86_400);
    let (y, mo, d) = super::civil_from_days(secs.div_euclid(86_400));
    let (h, mi, s) = (rem / 3600, (rem % 3600) / 60, rem % 60);
    format!("{y:04}{mo:02}{d:02}-{h:02}{mi:02}{s:02}")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn timestamp_is_compact_utc() {
        let ts = compact_timestamp();
        assert_eq!(ts.len(), 15, "expected YYYYmmdd-HHMMSS, got {ts}");
        assert_eq!(&ts[8..9], "-");
        assert!(ts.chars().filter(|c| c.is_ascii_digit()).count() == 14);
    }

    #[test]
    fn with_suffix_appends_to_final_component() {
        assert_eq!(
            with_suffix(Path::new("/a/b/app.db"), ".restore.tmp"),
            PathBuf::from("/a/b/app.db.restore.tmp")
        );
    }

    /// `std::fs::rename` (used directly by `restore_from_blob`) already
    /// replaces an existing destination on both Unix and Windows — Rust's
    /// standard library always passes `MOVEFILE_REPLACE_EXISTING` to
    /// `MoveFileExW` on Windows, so this has never required a manual
    /// remove-then-retry workaround on either platform. Guards the actual
    /// entry point `restore_from_blob` relies on (rather than testing a
    /// standalone rename-replace wrapper — see git history on this file
    /// for why one existed and was removed: it retried on
    /// `io::ErrorKind::AlreadyExists`, an error kind `fs::rename` cannot
    /// actually produce for this scenario on any supported platform).
    #[test]
    fn rename_overwrites_existing_destination_on_disk() {
        let dir = std::env::temp_dir().join(format!(
            "backup-rename-replacing-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        fs::create_dir_all(&dir).unwrap();

        let from = dir.join("src.tmp");
        let to = dir.join("dest.db");
        fs::write(&from, b"new-content").unwrap();
        fs::write(&to, b"old-content").unwrap();

        fs::rename(&from, &to).expect("rename must overwrite existing destination");

        assert_eq!(fs::read(&to).unwrap(), b"new-content");
        assert!(
            !from.exists(),
            "source must be gone after a successful rename"
        );

        let _ = fs::remove_dir_all(&dir);
    }
}
