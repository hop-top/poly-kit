use std::path::Path;

use super::BoxError;

/// One versioned store (a database, a set of files, a directory) taking
/// part in migrations. Go `upgrade.SchemaDriver`.
///
/// The runner owns the canonical version file and calls
/// [`set_version`](Self::set_version) after writing it, so a driver may
/// read its version from that file
/// ([`read_version_file_in`](super::read_version_file_in)) and treat
/// `set_version` as a notification. A driver that keeps the version
/// itself (a database table) says so with
/// [`stores_version`](Self::stores_version): the runner then writes no
/// version file for it.
pub trait SchemaDriver: Send + Sync {
    /// Schema identifier, matched against [`Migration::schema`](super::Migration::schema).
    fn name(&self) -> &str;

    /// The applied version, `""` when none is recorded.
    fn version(&self) -> Result<String, BoxError>;

    /// Records `version`: after each applied migration, and the earlier
    /// version again after a rollback or restore.
    fn set_version(&mut self, version: &str) -> Result<(), BoxError>;

    /// `true` when the driver is the only authority for its version
    /// (Go `VersionStorer`): the runner calls only
    /// [`set_version`](Self::set_version) and writes or reads no
    /// version file. Defaults to `false`.
    fn stores_version(&self) -> bool {
        false
    }

    /// Copies the current state into the directory `dest`, which exists.
    fn backup(&self, dest: &Path) -> Result<(), BoxError>;

    /// Replaces the current state from the backup directory `src`.
    fn restore(&mut self, src: &Path) -> Result<(), BoxError>;
}
