//! Versioned state migrations and the release check: Go `core/upgrade`
//! without the download and binary replace (self-update is not ported).
//!
//! A [`Migration`] moves one schema to a semver version. A [`Migrator`]
//! runs the pending ones per [`SchemaDriver`] in version order, backs up
//! first, records the version reached and rolls back on failure. Version
//! order ([`compare_versions`]), [`pending`] and the driver trait are pure
//! and build on wasm32; the runner, its version files and backups, and
//! [`configfile`] touch the filesystem and are compiled off wasm32 only.

pub mod document;
mod driver;
mod migration;
mod version;

pub use document::{
    migrate_document, migrate_value, DocApplied, DocError, DocErrorKind, DocOptions, DocReport,
    DocStep,
};
pub use driver::SchemaDriver;
pub use migration::{pending, Migration, StepFn};
pub use version::compare_versions;

#[cfg(not(target_arch = "wasm32"))]
mod backup;
#[cfg(not(target_arch = "wasm32"))]
pub mod check;
#[cfg(not(target_arch = "wasm32"))]
pub mod configfile;
#[cfg(not(target_arch = "wasm32"))]
mod events;
#[cfg(not(target_arch = "wasm32"))]
mod fs_util;
#[cfg(not(target_arch = "wasm32"))]
mod migrator;

#[cfg(not(target_arch = "wasm32"))]
pub use events::{MigrationPublisher, MigrationTopics};
#[cfg(not(target_arch = "wasm32"))]
pub use migrator::{
    default_data_dir, read_version_file, read_version_file_in, AppliedMigration, FinalVerifyFn,
    MigrateError, Migrator, SchemaStatus, StepKind, WarnFn,
};

/// Error a migration step or a driver returns.
pub type BoxError = Box<dyn std::error::Error + Send + Sync>;
