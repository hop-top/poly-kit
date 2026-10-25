//! Layered YAML configuration. Ports Go `go/core/config`.
//!
//! Gated behind the `config` feature.

#[cfg(not(target_arch = "wasm32"))]
mod edit;
mod env;
mod error;
#[cfg(not(target_arch = "wasm32"))]
mod getter;
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
    ReloadFailReason, ReloadFailedPayload, ReloadOptions, ReloadPublisher, ReloadTopics,
    Reloadable, ReloadedPayload,
};
pub use resolved::{
    format_resolved, format_resolved_table, resolved_as_json, sorted_by_key, Origin, Resolved,
};
pub use scope::{scope_path, Scope};
#[cfg(not(target_arch = "wasm32"))]
pub use setter::{set, set_value, unset};
pub use value::{Map, Value};
