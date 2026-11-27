//! Backup and restore a driver cannot do: [`BackupUnsupported`] (Go
//! `ErrBackupUnsupported`) and the [`NotRestored`] error of a rollback
//! that restored nothing (Go `ErrNotRestored`).

use std::error::Error;
use std::fmt;

use super::BoxError;

/// What a [`SchemaDriver`](super::SchemaDriver)'s `backup` or `restore`
/// returns (boxed) when kit cannot back up or restore its store: a server
/// database, a file under a live connection pool. A `backup` that returns
/// it is no failure: the run goes on with no backup, keeps no backup
/// directory and prunes nothing. A `restore` that returns it restores
/// nothing. Test with [`is_backup_unsupported`] or
/// [`MigrateError::is_backup_unsupported`](super::MigrateError::is_backup_unsupported).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct BackupUnsupported;

impl fmt::Display for BackupUnsupported {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("upgrade: backup unsupported")
    }
}

impl Error for BackupUnsupported {}

/// A rollback that could not undo a failed run: a `down` was missing or
/// failed and no backup could restore the schema, so the stored version
/// stays at the last step still applied. It is the `rollback` of
/// [`MigrateError::Step`](super::MigrateError::Step) (or the source of
/// [`MigrateError::Rollback`](super::MigrateError::Rollback)); its
/// [`source`](Error::source) is the cause, typically [`BackupUnsupported`].
/// No reverted event is published for it. Test with [`is_not_restored`] or
/// [`MigrateError::is_not_restored`](super::MigrateError::is_not_restored).
#[derive(Debug)]
pub struct NotRestored {
    text: String,
    cause: BoxError,
}

impl NotRestored {
    pub(super) fn new(text: impl Into<String>, cause: BoxError) -> NotRestored {
        NotRestored {
            text: text.into(),
            cause,
        }
    }

    /// No backup was taken (its `backup` was unsupported).
    pub(super) fn no_backup(cause: BoxError) -> NotRestored {
        NotRestored::new("not restored: no backup was taken", cause)
    }

    /// The driver's `restore` was unsupported.
    pub(super) fn restore_unsupported(cause: BoxError) -> NotRestored {
        NotRestored::new("not restored: restore unsupported", cause)
    }
}

impl fmt::Display for NotRestored {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.text)
    }
}

impl Error for NotRestored {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        Some(&*self.cause)
    }
}

/// Whether `err`, or anything in its source chain, is [`BackupUnsupported`].
/// Go `errors.Is(err, ErrBackupUnsupported)`.
pub fn is_backup_unsupported(err: &(dyn Error + 'static)) -> bool {
    chain_has(err, |e| e.is::<BackupUnsupported>())
}

/// Whether `err`, or anything in its source chain, is [`NotRestored`].
/// Go `errors.Is(err, ErrNotRestored)`.
pub fn is_not_restored(err: &(dyn Error + 'static)) -> bool {
    chain_has(err, |e| e.is::<NotRestored>())
}

fn chain_has(err: &(dyn Error + 'static), pred: impl Fn(&(dyn Error + 'static)) -> bool) -> bool {
    let mut cur = Some(err);
    while let Some(e) = cur {
        if pred(e) {
            return true;
        }
        cur = e.source();
    }
    false
}

/// Wraps an unsupported `restore` error as [`NotRestored`]; other errors
/// pass through.
pub(super) fn restore_error(err: BoxError) -> BoxError {
    if is_backup_unsupported(&*err) {
        Box::new(NotRestored::restore_unsupported(err))
    } else {
        err
    }
}
