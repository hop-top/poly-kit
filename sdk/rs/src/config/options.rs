//! [`Options`]: the sources a load reads. Ports the `Options` struct of
//! Go `loader.go`.

use std::any::Any;
use std::fmt;
use std::path::PathBuf;
use std::sync::Arc;

use serde::Serialize;

use super::env::EnvBind;
use super::migrate::{MigrateOptions, Migration};
use super::yaml::{roundtrip, Node};
use super::{Error, Map};

/// Callback run on the merged config after files, before overrides.
/// Receives the destination as `&mut dyn Any`; downcast to the type
/// passed to [`load`](super::load), or use [`Options::env_override_for`].
pub type EnvOverride = Arc<dyn Fn(&mut dyn Any) + Send + Sync>;

/// Load sources. Empty paths are skipped.
///
/// Go's `Viper` field has no counterpart: `env_prefix` and `env_binds`
/// feed [`Options::env_lookup`], a key/value view with viper's env
/// lookup rules (see [`EnvLookup`](super::EnvLookup)).
#[derive(Clone, Default)]
pub struct Options {
    /// System-wide file (`/etc/tool/config.yaml`). Missing is skipped.
    pub system_config_path: PathBuf,
    /// Per-user file (`~/.config/tool/config.yaml`). Missing is skipped.
    pub user_config_path: PathBuf,
    /// Project file (`.tool.yaml`). Missing is skipped.
    pub project_config_path: PathBuf,
    /// Files merged after the project file, in order. These are explicit
    /// `-c <path>` requests, so a missing one is an error.
    pub extra_config_paths: Vec<PathBuf>,
    /// Run on each file before it merges.
    pub migrations: Vec<Migration>,
    pub migrate_options: MigrateOptions,
    /// See [`EnvOverride`].
    pub env_override: Option<EnvOverride>,
    /// `-c key=value` overrides, applied last; they win over everything.
    pub overrides: Option<Map>,
    /// Prefix for [`Options::env_lookup`]'s automatic mapping.
    pub env_prefix: String,
    /// Explicit key to env var bindings for [`Options::env_lookup`].
    pub env_binds: Vec<EnvBind>,
    /// Typed defaults seeded before any file; build with
    /// [`Defaults::of`] or [`Options::with_defaults`].
    pub defaults: Option<Defaults>,
}

/// Encoded defaults (Go's `Options.Defaults`), opaque.
#[derive(Clone, Debug)]
pub struct Defaults(pub(crate) Node);

impl Defaults {
    /// Encodes `defaults` (usually a value of the config struct).
    pub fn of<T: Serialize>(defaults: &T) -> Result<Defaults, Error> {
        Ok(Defaults(
            roundtrip(defaults).map_err(|e| e.wrap("marshal defaults"))?,
        ))
    }
}

impl fmt::Debug for Options {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Options")
            .field("system_config_path", &self.system_config_path)
            .field("user_config_path", &self.user_config_path)
            .field("project_config_path", &self.project_config_path)
            .field("extra_config_paths", &self.extra_config_paths)
            .field("migrations", &self.migrations.len())
            .field("env_override", &self.env_override.is_some())
            .field("overrides", &self.overrides)
            .field("env_prefix", &self.env_prefix)
            .field("env_binds", &self.env_binds)
            .field("defaults", &self.defaults.is_some())
            .finish()
    }
}

impl Options {
    /// Seeds the destination with `defaults` before any file (Go's
    /// `Options.Defaults`). Merge rules are a file layer's: scalars and
    /// lists replaced, struct fields merged, map entries replaced.
    pub fn with_defaults<T: Serialize>(mut self, defaults: &T) -> Result<Options, Error> {
        self.defaults = Some(Defaults::of(defaults)?);
        Ok(self)
    }

    /// Typed [`EnvOverride`]: `f` runs only when the destination is `T`.
    pub fn env_override_for<T: 'static>(
        mut self,
        f: impl Fn(&mut T) + Send + Sync + 'static,
    ) -> Options {
        self.env_override = Some(Arc::new(move |cfg: &mut dyn Any| {
            if let Some(c) = cfg.downcast_mut::<T>() {
                f(c);
            }
        }));
        self
    }

    /// Whether defaults are set.
    pub fn has_defaults(&self) -> bool {
        self.defaults.is_some()
    }
}
