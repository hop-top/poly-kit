use std::fmt;
use std::fs;
use std::io;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Instant;

use super::backup::{Backups, LatestError, DEFAULT_RETENTION};
use super::events::{MigrationPublisher, MigrationTopics, Payload, SOURCE};
use super::{fs_util, pending, BoxError, Migration, SchemaDriver};
use crate::bus::{Context, Event, Topic};

/// Which part of a migration stopped a run. Go tells them apart with
/// `errors.Is(err, ErrVerify)` and `errors.Is(err, context.Canceled)`.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum StepKind {
    /// The migration's `up` failed.
    Up,
    /// The migration's `verify` failed.
    Verify,
    /// The migrator's final verify failed.
    FinalVerify,
    /// The context was done before the migration's `up`.
    Canceled,
}

impl StepKind {
    fn stage(self) -> &'static str {
        match self {
            StepKind::Up => "up",
            StepKind::Verify => "verify",
            StepKind::FinalVerify => "final",
            StepKind::Canceled => "canceled",
        }
    }
}

fn step_text(
    kind: StepKind,
    version: &str,
    source: &BoxError,
    rollback: &Option<BoxError>,
) -> String {
    let head = match kind {
        StepKind::Up => format!("up {version} failed"),
        StepKind::Verify => format!("verify {version} failed"),
        StepKind::FinalVerify => format!("final verify {version} failed"),
        StepKind::Canceled => format!("up {version} canceled"),
    };
    match rollback {
        Some(r) => format!("{head}: {source} (rollback also failed: {r})"),
        None => format!("{head}: {source}"),
    }
}

/// What a [`Migrator`] run or rollback failed on. The texts match Go's.
#[derive(Debug, thiserror::Error)]
pub enum MigrateError {
    #[error("migrate {schema}: read version: {source}")]
    ReadVersion { schema: String, source: BoxError },
    #[error("migrate {schema}: prepare backup: create backup dir: {source}")]
    PrepareBackup { schema: String, source: io::Error },
    #[error("migrate {schema}: backup: {source}")]
    Backup { schema: String, source: BoxError },
    /// A migration step stopped the run. With auto-rollback the schema
    /// was rolled back, unless `rollback` says how that failed too.
    #[error("migrate {schema}: {}", step_text(*.kind, .version, .source, .rollback))]
    Step {
        schema: String,
        kind: StepKind,
        /// The version being attempted (the last one for a final verify).
        version: String,
        source: BoxError,
        rollback: Option<BoxError>,
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

impl MigrateError {
    /// The step kind, for [`MigrateError::Step`].
    pub fn step_kind(&self) -> Option<StepKind> {
        match self {
            MigrateError::Step { kind, .. } => Some(*kind),
            _ => None,
        }
    }

    /// A verify (per migration or final) failed. Go `errors.Is(err, ErrVerify)`.
    pub fn is_verify(&self) -> bool {
        matches!(
            self.step_kind(),
            Some(StepKind::Verify | StepKind::FinalVerify)
        )
    }

    /// The context was done between steps. Go `errors.Is(err, context.Canceled)`.
    pub fn is_canceled(&self) -> bool {
        self.step_kind() == Some(StepKind::Canceled)
    }
}

/// The final-state check behind [`FinalVerifyFn`].
pub type FinalVerify = dyn Fn(&Context, &str, &str) -> Result<(), BoxError> + Send + Sync;

/// Final-state check: `(ctx, schema, version)`. Go `WithVerify`.
pub type FinalVerifyFn = Arc<FinalVerify>;

/// Receives warnings (a failed backup prune). Defaults to stderr, as Go's `log.Printf`.
pub type WarnFn = Arc<dyn Fn(&str) + Send + Sync>;

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
/// `pre-<first pending version>`, apply each `up` then `verify` in order,
/// run the final [`verify`](Self::verify), then write the last applied
/// version to `<data dir>/<schema>/version` and tell the driver. A failed
/// step, or a context done before the next `up`, stops the run; with
/// auto-rollback (the default) the applied migrations' `down`s run newest
/// first, and the backup is restored instead as soon as one is missing or
/// fails.
pub struct Migrator {
    tool: String,
    version: String,
    data_dir: Option<PathBuf>,
    auto_rollback: bool,
    retention: usize,
    migrations: Vec<Migration>,
    drivers: Vec<Box<dyn SchemaDriver>>,
    history: Vec<AppliedMigration>,
    verify: Option<FinalVerifyFn>,
    publisher: Option<Arc<dyn MigrationPublisher>>,
    topics: MigrationTopics,
    warn: WarnFn,
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
            .field("verify", &self.verify.is_some())
            .field("publisher", &self.publisher.is_some())
            .field("topics", &self.topics)
            .finish_non_exhaustive()
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
            verify: None,
            publisher: None,
            topics: MigrationTopics::default(),
            warn: Arc::new(|w| eprintln!("{w}")),
        }
    }

    /// Checks each schema's final state after its last pending migration,
    /// before the version is recorded. A failure rolls back every
    /// migration applied to that schema in the run. Go `WithVerify`.
    pub fn verify(
        mut self,
        f: impl Fn(&Context, &str, &str) -> Result<(), BoxError> + Send + Sync + 'static,
    ) -> Migrator {
        self.verify = Some(Arc::new(f));
        self
    }

    /// Publishes lifecycle events to `p` (a kit bus, or a closure),
    /// synchronously and in order. Without one a migrator is silent. Go
    /// `WithEventPublisher`.
    pub fn publisher(mut self, p: impl MigrationPublisher + 'static) -> Migrator {
        self.publisher = Some(Arc::new(p));
        self
    }

    /// Replaces the event topics; see [`MigrationTopics::from_prefix`]
    /// and [`MigrationTopics::or_default`].
    pub fn topics(mut self, topics: MigrationTopics) -> Migrator {
        self.topics = topics;
        self
    }

    /// Receives warnings instead of stderr.
    pub fn on_warning(mut self, f: impl Fn(&str) + Send + Sync + 'static) -> Migrator {
        self.warn = Arc::new(f);
        self
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
    /// failing one, or before the next `up` once `ctx` is done.
    /// Re-running after success applies nothing. Go `Run`.
    pub fn run(&mut self, ctx: &Context) -> Result<(), MigrateError> {
        let start = Instant::now();
        let ev = Emitter {
            tool: &self.tool,
            publisher: self.publisher.as_deref(),
            topics: &self.topics,
        };
        ev.emit(
            &ev.topics.run_started,
            ev.payload().set("to", self.version.as_str()),
        );
        let data_dir = self.resolved_data_dir();
        let mut applied = 0usize;
        for d in &mut self.drivers {
            let mut run = SchemaRun {
                migrations: &self.migrations,
                target: &self.version,
                data_dir: &data_dir,
                auto_rollback: self.auto_rollback,
                retention: self.retention,
                history: &mut self.history,
                verify: self.verify.as_deref(),
                ev: &ev,
                warn: &*self.warn,
                applied: 0,
            };
            let result = run.run(d.as_mut(), ctx);
            applied += run.applied;
            if let Err(e) = result {
                ev.emit(
                    &ev.topics.run_failed,
                    ev.payload()
                        .set("to", self.version.as_str())
                        .set("schema", d.name())
                        .set("error", e.to_string())
                        .set("duration_ms", millis(start)),
                );
                return Err(e);
            }
        }
        ev.emit(
            &ev.topics.run_completed,
            ev.payload()
                .set("to", self.version.as_str())
                .set("applied", applied)
                .set("duration_ms", millis(start)),
        );
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

fn millis(since: Instant) -> u64 {
    u64::try_from(since.elapsed().as_millis()).unwrap_or(u64::MAX)
}

struct Emitter<'a> {
    tool: &'a str,
    publisher: Option<&'a dyn MigrationPublisher>,
    topics: &'a MigrationTopics,
}

impl Emitter<'_> {
    fn payload(&self) -> Payload {
        Payload::new(self.tool)
    }

    fn emit(&self, topic: &Topic, payload: Payload) {
        if let Some(p) = self.publisher {
            if !topic.as_str().is_empty() {
                let event = Event::new(topic.clone(), SOURCE, payload.into_value());
                p.publish(&Context::background(), &event);
            }
        }
    }

    fn step(&self, schema: &str, from: &str, to: &str) -> Payload {
        self.payload()
            .set("schema", schema)
            .set("from", from)
            .set("to", to)
    }
}

/// What stopped a schema's run, with what to roll back.
struct Failure<'m> {
    kind: StepKind,
    version: String,
    source: BoxError,
    undo: Vec<&'m Migration>,
}

struct SchemaRun<'a> {
    migrations: &'a [Migration],
    target: &'a str,
    data_dir: &'a Path,
    auto_rollback: bool,
    retention: usize,
    history: &'a mut Vec<AppliedMigration>,
    verify: Option<&'a FinalVerify>,
    ev: &'a Emitter<'a>,
    warn: &'a (dyn Fn(&str) + Send + Sync),
    applied: usize,
}

impl<'a> SchemaRun<'a> {
    fn run(&mut self, d: &mut dyn SchemaDriver, ctx: &Context) -> Result<(), MigrateError> {
        let schema = d.name().to_string();
        let current = d.version().map_err(|source| MigrateError::ReadVersion {
            schema: schema.clone(),
            source,
        })?;
        let todo = pending(self.migrations, &schema, &current, self.target);
        let (Some(first), Some(last)) = (todo.first(), todo.last()) else {
            return Ok(());
        };

        let backups = Backups::new(self.data_dir, &schema, self.retention);
        let backup =
            backups
                .create(&first.version)
                .map_err(|source| MigrateError::PrepareBackup {
                    schema: schema.clone(),
                    source,
                })?;
        d.backup(&backup).map_err(|source| MigrateError::Backup {
            schema: schema.clone(),
            source,
        })?;

        if let Err(f) = self.apply_all(ctx, &schema, &current, &todo) {
            return Err(self.fail(d, &schema, &current, &backup, f));
        }

        let version_file = |step: &'static str| {
            let schema = schema.clone();
            move |source| MigrateError::VersionFile {
                schema,
                step,
                source,
            }
        };
        let dir = self.data_dir.join(&schema);
        fs_util::create_dir_all(&dir).map_err(version_file("create version dir"))?;
        fs_util::write_private(&dir.join("version"), last.version.as_bytes())
            .map_err(version_file("write version file"))?;
        d.set_version(&last.version)
            .map_err(|source| MigrateError::SetVersion {
                schema: schema.clone(),
                source,
            })?;

        // Non-fatal, as in Go, which logs it.
        if let Err(e) = backups.prune() {
            (self.warn)(&format!("warning: prune old backups for {schema}: {e}"));
        }
        Ok(())
    }

    fn apply_all<'m>(
        &mut self,
        ctx: &Context,
        schema: &str,
        current: &str,
        todo: &[&'m Migration],
    ) -> Result<(), Failure<'m>> {
        let ev = self.ev;
        let mut from = current.to_string();
        let mut done: Vec<&'m Migration> = Vec::new();
        for &m in todo {
            let base = || ev.step(schema, &from, &m.version);
            if let Some(err) = ctx.err() {
                return Err(self.failed(
                    base(),
                    StepKind::Canceled,
                    &m.version,
                    Box::new(err),
                    done,
                ));
            }
            ev.emit(&ev.topics.started, base());
            let t = Instant::now();
            if let Err(e) = (m.up)(ctx) {
                let p = base().set("duration_ms", millis(t));
                return Err(self.failed(p, StepKind::Up, &m.version, e, done));
            }
            ev.emit(&ev.topics.applied, base().set("duration_ms", millis(t)));
            if let Some(verify) = &m.verify {
                let t = Instant::now();
                if let Err(e) = verify(ctx) {
                    // `up` completed: the rollback must undo this step too.
                    let mut undo = done;
                    undo.push(m);
                    let p = base().set("duration_ms", millis(t));
                    return Err(self.failed(p, StepKind::Verify, &m.version, e, undo));
                }
                ev.emit(&ev.topics.verified, base().set("duration_ms", millis(t)));
            }
            done.push(m);
            self.applied += 1;
            self.history.push(AppliedMigration {
                version: m.version.clone(),
                schema: m.schema.clone(),
            });
            from = m.version.clone();
        }
        if let Some(verify) = self.verify {
            let p = ev.step(schema, current, &from);
            let t = Instant::now();
            if let Err(e) = verify(ctx, schema, &from) {
                let p = p.set("duration_ms", millis(t));
                return Err(self.failed(p, StepKind::FinalVerify, &from, e, done));
            }
            ev.emit(
                &ev.topics.verified,
                p.set("final", true).set("duration_ms", millis(t)),
            );
        }
        Ok(())
    }

    fn failed<'m>(
        &self,
        payload: Payload,
        kind: StepKind,
        version: &str,
        source: BoxError,
        undo: Vec<&'m Migration>,
    ) -> Failure<'m> {
        self.ev.emit(
            &self.ev.topics.failed,
            payload
                .set("stage", kind.stage())
                .set("error", source.to_string()),
        );
        Failure {
            kind,
            version: version.to_string(),
            source,
            undo,
        }
    }

    /// Rolls back in auto mode and builds the schema's error.
    fn fail(
        &self,
        d: &mut dyn SchemaDriver,
        schema: &str,
        current: &str,
        backup: &Path,
        f: Failure<'_>,
    ) -> MigrateError {
        let mut rollback = None;
        if self.auto_rollback && !f.undo.is_empty() {
            let t = Instant::now();
            match roll_back(d, &f.undo, backup) {
                Ok(method) => self.ev.emit(
                    &self.ev.topics.reverted,
                    self.ev
                        .step(schema, &f.version, current)
                        .set("method", method)
                        .set("duration_ms", millis(t)),
                ),
                Err(e) => rollback = Some(e),
            }
        }
        MigrateError::Step {
            schema: schema.to_string(),
            kind: f.kind,
            version: f.version,
            source: f.source,
            rollback,
        }
    }
}

/// `down` of each applied migration, newest first, on an uncanceled
/// context; the backup is restored instead the moment one is missing or
/// fails. Reports which of the two undid the changes.
fn roll_back(
    d: &mut dyn SchemaDriver,
    applied: &[&Migration],
    backup: &Path,
) -> Result<&'static str, BoxError> {
    let ctx = Context::background();
    for m in applied.iter().rev() {
        match &m.down {
            Some(down) if down(&ctx).is_ok() => {}
            _ => return d.restore(backup).map(|()| "restore"),
        }
    }
    Ok("down")
}
