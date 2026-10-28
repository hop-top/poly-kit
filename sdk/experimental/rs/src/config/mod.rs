//! Layered YAML configuration. Ports Go `go/core/config`.
//!
//! Gated behind the `config` feature. Merge rules, precedence, scalar
//! typing, override syntax, migrations, defaults, partition and reload
//! follow Go and are pinned by the shared `contracts/config-v1` corpus.
//! The pure parts (YAML model, merge, overrides, precedence,
//! [`parse_scalar`], [`Value`], [`partition`]) build on wasm32; files,
//! paths, `set`/`unset` and reload do not. Hot reload on a signal is
//! feature `config-signal` (Unix); PKL schemas are feature `config-pkl`.
//!
//! # Example
//!
//! ```no_run
//! use hop_top_kit::config::{load, options_for_tool};
//!
//! #[derive(Default, serde::Serialize, serde::Deserialize)]
//! #[serde(default)]
//! struct Cfg {
//!     name: String,
//!     port: i64,
//! }
//!
//! let mut cfg = Cfg::default();
//! load(&mut cfg, &options_for_tool("mytool")).unwrap();
//! ```
//!
//! # Differences from Go
//!
//! - **Writes keep bytes, not Go's layout.** [`set`], [`set_value`] and
//!   [`unset`] splice only the targeted entry, so comments, anchors and
//!   formatting survive; values, kept comments and key order match Go,
//!   bytes do not; `\r\n` files stay `\r\n` (Go writes `\n`). An edit
//!   the splicer cannot make without losing a comment or anchor outside
//!   the edited entry (explicit `? key` entries, for one) is refused with
//!   [`ErrorKind::EditUnsupported`] and the file is left untouched; Go
//!   never refuses. So is an `unset` that would remove an anchor still
//!   aliased elsewhere, where Go writes a file it cannot read back. A
//!   non-mapping document root is an error (Go silently writes nothing
//!   useful); unsetting the last key leaves an empty file where Go writes
//!   `{}`.
//! - **No viper.** `Options.Viper`, `BindEnv`, `SeedDefaults` and
//!   `SyncToViper` become [`EnvLookup`] ([`bind_env`],
//!   [`EnvLookup::seed_defaults`], [`EnvLookup::sync`],
//!   [`Options::env_lookup`]) with viper's lookup order.
//! - **No struct tags.** Reload mutability comes from [`ReloadFields`]
//!   instead of `reload:"true"`; field names are the serde names. Config
//!   structs should derive `Default` with `#[serde(default)]`, which Go
//!   gets implicitly from zero values.
//! - **Typing.** Timestamps stay text (Go decodes `time.Time`);
//!   non-string mapping keys become text keys (Go builds `map[any]any`); a
//!   [`Value`]-typed field holding a mapping merges per key where Go
//!   replaces an `any` field whole. Decode errors keep yaml.v3's shape
//!   but name Rust types (`int` for `i64`, `[]` for lists).
//! - **Parser.** YAML is parsed by saphyr-parser (YAML 1.2) with yaml.v3's
//!   tag resolution on top; syntax error texts differ from libyaml's.
//!   Inputs libyaml reads but saphyr-parser 0.2.1 rejects or misreads
//!   are made readable first, in one linear pass: a leading BOM (saphyr
//!   does not skip it; edits keep it), a tab after `:`, and quoted-scalar
//!   continuation lines at or left of the block indent or led by a tab.
//!   A tab between a sequence `-` and its entry, which saphyr accepts, is
//!   rejected as libyaml does. Implicit empty keys are rejected as in
//!   libyaml. Other exotic inputs libyaml rejects may parse. Decoding
//!   keeps yaml.v3's duplicate-key check (per file, before merging), its
//!   non-scalar-key error and its alias budget (`document contains
//!   excessive aliasing`).
//! - **Callbacks.** [`Options::env_override`] receives `&mut dyn Any`
//!   ([`Options::env_override_for`] downcasts); a migration cannot return
//!   a nil map; `watch_signal` is async, ends with `Ok(())` when its
//!   shutdown future completes, and takes tokio signal kinds.
//! - **Scope.** [`crate::xdg`] ports only the home and config-home
//!   resolution, without the write guard. The PKL wizard, `RunWizard` and
//!   the config command are deferred until kit-rs has a console wizard and
//!   a CLI root.

mod bus_getter;
#[cfg(not(target_arch = "wasm32"))]
pub(crate) mod edit;
mod env;
mod error;
#[cfg(not(target_arch = "wasm32"))]
pub(crate) mod getter;
mod goquote;
#[cfg(not(target_arch = "wasm32"))]
mod loader;
mod merge;
mod migrate;
#[cfg(not(target_arch = "wasm32"))]
mod nearest;
mod options;
mod overrides;
mod parse_scalar;
mod partition;
#[cfg(not(target_arch = "wasm32"))]
mod paths;
#[cfg(feature = "config-pkl")]
pub mod pkl;
mod precedence;
mod registry;
#[cfg(not(target_arch = "wasm32"))]
mod reload;
mod resolved;
mod scope;
#[cfg(not(target_arch = "wasm32"))]
mod setter;
#[cfg(all(feature = "config-signal", unix))]
mod signal;
mod value;
pub(crate) mod yaml;

pub use env::{bind_env, EnvBind, EnvLookup};
pub use error::{Error, ErrorKind, SchemaError};
#[cfg(not(target_arch = "wasm32"))]
pub use getter::{get, list, Entry};
#[cfg(not(target_arch = "wasm32"))]
pub use loader::load;
#[cfg(not(target_arch = "wasm32"))]
pub use migrate::migrate;
pub use migrate::{
    apply_migrations, MigrateError, MigrateFn, MigrateOptions, Migration, OnMigration,
};
#[cfg(not(target_arch = "wasm32"))]
pub use nearest::{nearest, nearest_with_depth, DEFAULT_MAX_DEPTH};
pub use options::{Defaults, EnvOverride, Options};
#[cfg(not(target_arch = "wasm32"))]
pub use overrides::parse_config_args;
pub use overrides::{apply_overrides, parse_overrides, with_project_marker, ParseOption};
pub use parse_scalar::parse_scalar;
pub use partition::{partition, Field, FieldDiff, ReloadFields};
#[cfg(not(target_arch = "wasm32"))]
pub use paths::{
    options_for_tool, options_for_tool_with_markers, options_for_tool_with_markers_from, paths,
    paths_for_tool, paths_for_tool_with_markers, ResolvedPath,
};
#[cfg(not(target_arch = "wasm32"))]
pub use precedence::load_yaml_layer;
pub use precedence::{
    resolve_string, yaml_layer_from_str, DefaultLayer, EnvFn, EnvLayer, FlagLayer, Layer, YamlLayer,
};
#[cfg(not(target_arch = "wasm32"))]
pub use registry::{load_registry, save_registry};
pub use registry::{Registry, Source};
#[cfg(not(target_arch = "wasm32"))]
pub use reload::{
    check_reload_fields, ReloadFailReason, ReloadFailedPayload, ReloadOptions, ReloadPublisher,
    ReloadTopics, Reloadable, ReloadedPayload,
};
pub use resolved::{
    format_resolved, format_resolved_table, resolved_as_json, sorted_by_key, Origin, Resolved,
};
pub use scope::{scope_path, Scope};
#[cfg(not(target_arch = "wasm32"))]
pub use setter::{set, set_value, unset};
pub use value::{Map, Value};
