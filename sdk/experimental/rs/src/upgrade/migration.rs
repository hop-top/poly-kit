use std::fmt;
use std::sync::Arc;

use super::{compare_versions, BoxError};
use crate::bus::Context;

/// One migration step. Captures what it changes; called at most once
/// per run, with the run's context (a rollback gets an uncanceled one).
pub type StepFn = Arc<dyn Fn(&Context) -> Result<(), BoxError> + Send + Sync>;

/// A versioned migration of one schema. Go `upgrade.Migration`.
#[derive(Clone)]
pub struct Migration {
    /// Semver, pre-release included; the state this migration produces.
    pub version: String,
    /// Matches [`SchemaDriver::name`](super::SchemaDriver::name).
    pub schema: String,
    pub up: StepFn,
    /// Undoes `up`; `None` makes a rollback restore the backup instead.
    pub down: Option<StepFn>,
    /// Checks the result after `up`, before the version is recorded. A
    /// failure is a failed step, and the rollback includes this one.
    pub verify: Option<StepFn>,
}

impl Migration {
    pub fn new(
        version: impl Into<String>,
        schema: impl Into<String>,
        up: impl Fn(&Context) -> Result<(), BoxError> + Send + Sync + 'static,
    ) -> Migration {
        Migration {
            version: version.into(),
            schema: schema.into(),
            up: Arc::new(up),
            down: None,
            verify: None,
        }
    }

    pub fn with_down(
        mut self,
        down: impl Fn(&Context) -> Result<(), BoxError> + Send + Sync + 'static,
    ) -> Migration {
        self.down = Some(Arc::new(down));
        self
    }

    /// Go `Migration.Verify`.
    pub fn with_verify(
        mut self,
        verify: impl Fn(&Context) -> Result<(), BoxError> + Send + Sync + 'static,
    ) -> Migration {
        self.verify = Some(Arc::new(verify));
        self
    }
}

impl fmt::Debug for Migration {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Migration")
            .field("version", &self.version)
            .field("schema", &self.schema)
            .field("down", &self.down.is_some())
            .field("verify", &self.verify.is_some())
            .finish_non_exhaustive()
    }
}

/// The migrations of `schema` with `from < version <= to`, ascending.
/// Equal versions keep their registration order. Go `pendingMigrations`.
pub fn pending<'a>(
    migrations: &'a [Migration],
    schema: &str,
    from: &str,
    to: &str,
) -> Vec<&'a Migration> {
    let mut out: Vec<&Migration> = migrations
        .iter()
        .filter(|m| {
            m.schema == schema
                && compare_versions(&m.version, from).is_gt()
                && compare_versions(&m.version, to).is_le()
        })
        .collect();
    out.sort_by(|a, b| compare_versions(&a.version, &b.version));
    out
}
