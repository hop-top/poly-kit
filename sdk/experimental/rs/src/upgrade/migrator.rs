use std::fmt;
use std::fs;
use std::io;
use std::path::{Path, PathBuf};

use super::backup::{Backups, LatestError, DEFAULT_RETENTION};
use super::{fs_util, pending, BoxError, Migration, SchemaDriver};

/// What a [`Migrator`] run or rollback failed on. The texts match Go's.
#[derive(Debug, thiserror::Error)]
pub enum MigrateError {
    #[error("migrate {schema}: read version: {source}")]
    ReadVersion { schema: String, source: BoxError },
    #[error("migrate {schema}: prepare backup: create backup dir: {source}")]
    PrepareBackup { schema: String, source: io::Error },
    #[error("migrate {schema}: backup: {source}")]
    Backup { schema: String, source: BoxError },
    /// An `up` failed; with auto-rollback the schema was rolled back.
    #[error("migrate {schema}: up {version} failed: {source}")]
    Up {
        schema: String,
        version: String,
        source: BoxError,
    },
    /// An `up` failed and so did the rollback after it.
    #[error("migrate {schema}: up {version} failed: {source} (rollback also failed: {rollback})")]
    UpAndRollback {
        schema: String,
        version: String,
        source: BoxError,
        rollback: BoxError,
    },
    /// The version file could not be written; `step` is
    /// `create version dir` or `write version file`.
    #[error("migrate {schema}: set version: {step}: {source}")]
    VersionFile {
        schema: String,
        step: &'static str,
        source: io::Error,
    },
    /// The driver refused the new version (after the version file was written).
    #[error("migrate {schema}: set version: {source}")]
    SetVersion { schema: String, source: BoxError },
    #[error("rollback: only available in manual rollback mode")]
    RollbackUnavailable,
    #[error("rollback {schema}: {source}")]
    Rollback { schema: String, source: BoxError },
}

/// A migration applied in this session. Go `AppliedMigration`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AppliedMigration {
    pub version: String,
    pub schema: String,
}

/// Migration state of one schema. Go `SchemaStatus`.
#[derive(Debug)]
pub struct SchemaStatus {
    pub schema: String,
    /// Stored version; `""` when none, or when reading it failed.
    pub current: String,
    pub target: String,
    pub pending: usize,
    /// Why the stored version could not be read.
    pub error: Option<BoxError>,
}

/// Runs versioned migrations across schema drivers. Go `Migrator`.
///
/// Per driver, in the order added: read the stored version, collect the
/// pending migrations up to the target, back up into
/// `pre-<first pending version>`, apply each `up` in order, then write
/// the last applied version to `<data dir>/<schema>/version` and tell the
/// driver. A failed `up` stops the run; with auto-rollback (the default)
/// the applied migrations' `down`s run newest first, and the backup is
/// restored instead as soon as one is missing or fails.
pub struct Migrator {
    tool: String,
    version: String,
    data_dir: Option<PathBuf>,
    auto_rollback: bool,
    retention: usize,
    migrations: Vec<Migration>,
    drivers: Vec<Box<dyn SchemaDriver>>,
    history: Vec<AppliedMigration>,
}

impl fmt::Debug for Migrator {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let drivers: Vec<&str> = self.drivers.iter().map(|d| d.name()).collect();
        f.debug_struct("Migrator")
            .field("tool", &self.tool)
            .field("version", &self.version)
            .field("data_dir", &self.data_dir)
            .field("auto_rollback", &self.auto_rollback)
            .field("retention", &self.retention)
            .field("migrations", &self.migrations)
            .field("drivers", &drivers)
            .field("history", &self.history)
            .finish()
    }
}

/// `<data home>/hop/<tool>`, where a [`Migrator`] keeps version files and
/// backups unless [`Migrator::data_dir`] says otherwise.
pub fn default_data_dir(tool: &str) -> PathBuf {
    crate::xdg::data_home().join("hop").join(tool)
}

/// The version recorded for `schema` under `data_dir`, trimmed; `""`
/// when there is no version file.
pub fn read_version_file_in(data_dir: &Path, schema: &str) -> io::Result<String> {
    match fs::read_to_string(data_dir.join(schema).join("version")) {
        Ok(s) => Ok(s.trim().to_string()),
        Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(String::new()),
        Err(e) => Err(e),
    }
}

/// [`read_version_file_in`] under [`default_data_dir`]. Go `ReadVersionFile`.
pub fn read_version_file(tool: &str, schema: &str) -> io::Result<String> {
    read_version_file_in(&default_data_dir(tool), schema)
}

impl Migrator {
    /// A migrator for `tool` up to `version` (a leading `v` is dropped).
    pub fn new(tool: impl Into<String>, version: &str) -> Migrator {
        Migrator {
            tool: tool.into(),
            version: version.strip_prefix('v').unwrap_or(version).to_string(),
            data_dir: None,
            auto_rollback: true,
            retention: DEFAULT_RETENTION,
            migrations: Vec::new(),
            drivers: Vec::new(),
            history: Vec::new(),
        }
    }

    /// No rollback on failure; call [`rollback_latest`](Self::rollback_latest).
    /// Go `WithManualRollback`.
    pub fn manual_rollback(mut self) -> Migrator {
        self.auto_rollback = false;
        self
    }

    /// Backups kept per schema after a successful run; 0 means the
    /// default, 5. Go `WithBackupRetention`.
    pub fn backup_retention(mut self, n: usize) -> Migrator {
        self.retention = n;
        self
    }

    /// Keeps version files and backups under `dir` instead of
    /// [`default_data_dir`].
    pub fn data_dir(mut self, dir: impl Into<PathBuf>) -> Migrator {
        self.data_dir = Some(dir.into());
        self
    }

    /// Adds a migration. Go `RegisterMigration`, scoped to this migrator.
    pub fn register(&mut self, m: Migration) {
        self.migrations.push(m);
    }

    /// Adds a schema driver; drivers run in the order added.
    pub fn add_driver(&mut self, d: impl SchemaDriver + 'static) {
        self.drivers.push(Box::new(d));
    }

    pub fn tool(&self) -> &str {
        &self.tool
    }

    /// The target version, without a leading `v`.
    pub fn version(&self) -> &str {
        &self.version
    }

    /// The directory holding version files and backups.
    pub fn resolved_data_dir(&self) -> PathBuf {
        self.data_dir
            .clone()
            .unwrap_or_else(|| default_data_dir(&self.tool))
    }

    /// Migrations applied by this migrator, in order. A migration rolled
    /// back after a later failure stays listed, as in Go.
    pub fn history(&self) -> &[AppliedMigration] {
        &self.history
    }

    /// Stored version and pending count per driver.
    pub fn status(&self) -> Vec<SchemaStatus> {
        self.drivers
            .iter()
            .map(|d| {
                let (current, error) = match d.version() {
                    Ok(v) => (v, None),
                    Err(e) => (String::new(), Some(e)),
                };
                SchemaStatus {
                    schema: d.name().to_string(),
                    pending: pending(&self.migrations, d.name(), &current, &self.version).len(),
                    current,
                    target: self.version.clone(),
                    error,
                }
            })
            .collect()
    }

    /// Runs pending migrations for every driver; stops at the first
    /// failing one. Re-running after success applies nothing.
    pub fn run(&mut self) -> Result<(), MigrateError> {
        let data_dir = self.resolved_data_dir();
        for d in &mut self.drivers {
            run_schema(
                d.as_mut(),
                &self.migrations,
                &self.version,
                &data_dir,
                self.auto_rollback,
                self.retention,
                &mut self.history,
            )?;
        }
        Ok(())
    }

    /// Restores every driver from its newest backup. Manual mode only;
    /// the version files are left as they are.
    pub fn rollback_latest(&mut self) -> Result<(), MigrateError> {
        if self.auto_rollback {
            return Err(MigrateError::RollbackUnavailable);
        }
        let data_dir = self.resolved_data_dir();
        for d in &mut self.drivers {
            let schema = d.name().to_string();
            let fail = |source: BoxError| MigrateError::Rollback {
                schema: schema.clone(),
                source,
            };
            let latest = Backups::new(&data_dir, &schema, self.retention)
                .latest()
                .map_err(|e: LatestError| fail(e.into()))?;
            d.restore(&latest).map_err(fail)?;
        }
        Ok(())
    }
}

fn run_schema(
    d: &mut dyn SchemaDriver,
    migrations: &[Migration],
    target: &str,
    data_dir: &Path,
    auto_rollback: bool,
    retention: usize,
    history: &mut Vec<AppliedMigration>,
) -> Result<(), MigrateError> {
    let schema = d.name().to_string();
    let current = d.version().map_err(|source| MigrateError::ReadVersion {
        schema: schema.clone(),
        source,
    })?;
    let todo = pending(migrations, &schema, &current, target);
    let (Some(first), Some(last)) = (todo.first(), todo.last()) else {
        return Ok(());
    };

    let backups = Backups::new(data_dir, &schema, retention);
    let backup = backups
        .create(&first.version)
        .map_err(|source| MigrateError::PrepareBackup {
            schema: schema.clone(),
            source,
        })?;
    d.backup(&backup).map_err(|source| MigrateError::Backup {
        schema: schema.clone(),
        source,
    })?;

    for (i, m) in todo.iter().enumerate() {
        if let Err(source) = (m.up)() {
            let version = m.version.clone();
            if auto_rollback {
                if let Err(rollback) = roll_back(d, &todo[..i], &backup) {
                    return Err(MigrateError::UpAndRollback {
                        schema,
                        version,
                        source,
                        rollback,
                    });
                }
            }
            return Err(MigrateError::Up {
                schema,
                version,
                source,
            });
        }
        history.push(AppliedMigration {
            version: m.version.clone(),
            schema: m.schema.clone(),
        });
    }

    let version_file = |step: &'static str| {
        let schema = schema.clone();
        move |source| MigrateError::VersionFile {
            schema,
            step,
            source,
        }
    };
    let dir = data_dir.join(&schema);
    fs_util::create_dir_all(&dir).map_err(version_file("create version dir"))?;
    fs_util::write_private(&dir.join("version"), last.version.as_bytes())
        .map_err(version_file("write version file"))?;
    d.set_version(&last.version)
        .map_err(|source| MigrateError::SetVersion {
            schema: schema.clone(),
            source,
        })?;

    // Non-fatal, as in Go, which only logs it.
    let _ = backups.prune();
    Ok(())
}

/// `down` of each applied migration, newest first; the backup is
/// restored instead the moment one is missing or fails.
fn roll_back(
    d: &mut dyn SchemaDriver,
    applied: &[&Migration],
    backup: &Path,
) -> Result<(), BoxError> {
    for m in applied.iter().rev() {
        match &m.down {
            Some(down) if down().is_ok() => {}
            _ => return d.restore(backup),
        }
    }
    Ok(())
}
