//! Layered YAML configuration. Ports Go `go/core/config`.
//!
//! Gated behind the `config` feature.

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
#[cfg(not(target_arch = "wasm32"))]
mod paths;
mod precedence;
mod registry;
mod resolved;
mod scope;
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
pub use resolved::{
    format_resolved, format_resolved_table, resolved_as_json, sorted_by_key, Origin, Resolved,
};
pub use scope::{scope_path, Scope};
pub use value::{Map, Value};
