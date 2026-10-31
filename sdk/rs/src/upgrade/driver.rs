use std::path::Path;

use super::BoxError;

/// One versioned store (a database, a set of files, a directory) taking
/// part in migrations. Go `upgrade.SchemaDriver`.
///
/// The runner owns the canonical version file and calls
/// [`set_version`](Self::set_version) after writing it, so a driver may
/// read its version from that file
/// ([`read_version_file_in`](super::read_version_file_in)) and treat
/// `set_version` as a notification.
pub trait SchemaDriver: Send + Sync {
    /// Schema identifier, matched against [`Migration::schema`](super::Migration::schema).
    fn name(&self) -> &str;

    /// The applied version, `""` when none is recorded.
    fn version(&self) -> Result<String, BoxError>;

    /// Records `version` after a successful run.
    fn set_version(&mut self, version: &str) -> Result<(), BoxError>;

    /// Copies the current state into the directory `dest`, which exists.
    fn backup(&self, dest: &Path) -> Result<(), BoxError>;

    /// Replaces the current state from the backup directory `src`.
    fn restore(&mut self, src: &Path) -> Result<(), BoxError>;
}
