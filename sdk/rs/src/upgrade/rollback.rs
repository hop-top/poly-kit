//! Taking the backup and the auto-rollback of one schema: Go `takeBackup` and `Migrator.rollback`.

use std::path::{Path, PathBuf};

use super::backup::Backups;
use super::migrator::{record, record_error, MigrateError};
use super::unsupported::{is_backup_unsupported, restore_error, NotRestored};
use super::{BoxError, Migration, SchemaDriver};
use crate::bus::Context;

/// What a schema's run can restore from: the backup directory, or none
/// with the error its `backup` returned ([`BackupUnsupported`](super::BackupUnsupported)).
pub(super) enum BackupState {
    Taken(PathBuf),
    Unsupported(BoxError),
}

/// `down` of each applied migration, newest first, on an uncanceled
/// context, recording the version before each once its `down` succeeded;
/// the backup is restored instead (and the starting version recorded) the
/// moment one is missing or fails. A failed record stops the rollback:
/// the store would not describe the data. With no backup, or a `restore`
/// that is unsupported, nothing is restored: the error is
/// [`NotRestored`], and the version stays at the last record, the step
/// whose `down` was missing or failed. Reports which of the two undid
/// the changes.
pub(super) fn roll_back(
    data_dir: &Path,
    d: &mut dyn SchemaDriver,
    current: &str,
    applied: &[&Migration],
    backup: BackupState,
) -> Result<&'static str, BoxError> {
    let ctx = Context::background();
    for (i, m) in applied.iter().enumerate().rev() {
        match &m.down {
            Some(down) if down(&ctx).is_ok() => {}
            _ => {
                let dir: &Path = match backup {
                    BackupState::Taken(ref p) => p,
                    BackupState::Unsupported(cause) => {
                        return Err(Box::new(NotRestored::no_backup(cause)))
                    }
                };
                d.restore(dir).map_err(restore_error)?;
                record(data_dir, d, current).map_err(|e| record_error(current, &e))?;
                return Ok("restore");
            }
        }
        let prev = if i == 0 {
            current
        } else {
            &applied[i - 1].version
        };
        record(data_dir, d, prev).map_err(|e| record_error(prev, &e))?;
    }
    Ok("down")
}

/// Makes the backup directory (named after the first pending
/// version), has the driver fill it and saves beside it the version
/// the run starts at. A `backup` that is unsupported is no failure:
/// the directory this call created is removed again, no version is
/// saved and the run goes on with no backup.
pub(super) fn take_backup(
    backups: &Backups,
    d: &mut dyn SchemaDriver,
    first: &str,
    current: &str,
) -> Result<BackupState, MigrateError> {
    let schema = d.name().to_string();
    let existed = backups.path(first).exists();
    let backup = backups
        .create(first)
        .map_err(|source| MigrateError::PrepareBackup {
            schema: schema.clone(),
            source,
        })?;
    if let Err(source) = d.backup(&backup) {
        if !is_backup_unsupported(&*source) {
            return Err(MigrateError::Backup { schema, source });
        }
        if !existed {
            let _ = std::fs::remove_dir_all(&backup);
        }
        return Ok(BackupState::Unsupported(source));
    }
    Backups::save_pre_run(&backup, current)
        .map_err(|source| MigrateError::SavePreRun { schema, source })?;
    Ok(BackupState::Taken(backup))
}
