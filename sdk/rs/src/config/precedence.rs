//! Precedence ladder for single string keys: flag, env, project,
//! global, default. Ports Go `precedence.go`.

use std::collections::HashMap;
use std::fmt;
use std::sync::Arc;

use super::{Map, Origin, Resolved, Value};

/// One rung of the ladder. `lookup` returning `Some("")` means "set to
/// empty"; `None` means absent, try the next layer.
pub trait Layer {
    fn origin(&self) -> Origin;
    fn lookup(&self, key: &str) -> Option<String>;
    /// Human pointer to the source for `key` (Go `detailFor`). Layers
    /// outside this module default to empty.
    fn detail(&self, _key: &str) -> String {
        String::new()
    }
}

/// Flag name to value. `changed`, when set, gates which keys count as
/// user-supplied (pflag's `Changed`); `None` considers every entry.
#[derive(Clone, Debug, Default)]
pub struct FlagLayer {
    pub flags: HashMap<String, String>,
    pub changed: Option<HashMap<String, bool>>,
}

impl Layer for FlagLayer {
    fn origin(&self) -> Origin {
        Origin::Flag
    }
    fn lookup(&self, key: &str) -> Option<String> {
        if let Some(changed) = &self.changed {
            if !changed.get(key).copied().unwrap_or(false) {
                return None;
            }
        }
        self.flags.get(key).cloned()
    }
    fn detail(&self, key: &str) -> String {
        format!("--{key}")
    }
}

/// Environment reader, injectable so tests need no process state.
pub type EnvFn = Arc<dyn Fn(&str) -> Option<String> + Send + Sync>;

/// Reads `<PREFIX>_<KEY>`, upper-cased, with `.` and `-` turned into `_`.
#[derive(Clone, Default)]
pub struct EnvLayer {
    pub prefix: String,
    env: Option<EnvFn>,
}

impl fmt::Debug for EnvLayer {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("EnvLayer")
            .field("prefix", &self.prefix)
            .finish_non_exhaustive()
    }
}

impl EnvLayer {
    /// Reads the process environment.
    pub fn new(prefix: impl Into<String>) -> EnvLayer {
        EnvLayer {
            prefix: prefix.into(),
            env: None,
        }
    }

    /// Reads through `env` instead of the process environment.
    pub fn with_env(prefix: impl Into<String>, env: EnvFn) -> EnvLayer {
        EnvLayer {
            prefix: prefix.into(),
            env: Some(env),
        }
    }

    /// The variable this layer reads for `key`.
    pub fn env_name(&self, key: &str) -> String {
        let k = key.to_uppercase().replace(['.', '-'], "_");
        if self.prefix.is_empty() {
            k
        } else {
            format!("{}_{k}", self.prefix.to_uppercase())
        }
    }
}

impl Layer for EnvLayer {
    fn origin(&self) -> Origin {
        Origin::Env
    }
    fn lookup(&self, key: &str) -> Option<String> {
        let name = self.env_name(key);
        match &self.env {
            Some(f) => f(&name),
            None => process_env(&name),
        }
    }
    fn detail(&self, key: &str) -> String {
        self.env_name(key)
    }
}

#[cfg(not(target_arch = "wasm32"))]
pub(crate) fn process_env(name: &str) -> Option<String> {
    std::env::var_os(name).and_then(|v| v.into_string().ok())
}

#[cfg(target_arch = "wasm32")]
pub(crate) fn process_env(_name: &str) -> Option<String> {
    None
}

/// A top-level YAML mapping (a project or global file). Only top-level
/// scalars answer; nested mappings render with Go's `%v`.
#[derive(Clone, Debug, Default)]
pub struct YamlLayer {
    pub data: Map,
    pub origin: Origin,
    /// Shown as the detail when set.
    pub path: String,
}

impl Layer for YamlLayer {
    fn origin(&self) -> Origin {
        self.origin
    }
    fn lookup(&self, key: &str) -> Option<String> {
        self.data.get(key).map(|v| match v {
            Value::Null => String::new(),
            v => v.to_string(),
        })
    }
    fn detail(&self, _key: &str) -> String {
        self.path.clone()
    }
}

/// Parses YAML text into a layer.
pub fn yaml_layer_from_str(
    src: &str,
    origin: Origin,
    path: &str,
) -> Result<YamlLayer, super::Error> {
    let data = match super::yaml::parse(src)? {
        None => Map::new(),
        Some(node) => {
            let v: Option<Map> = super::yaml::de::from_node(&node)?;
            v.unwrap_or_default()
        }
    };
    Ok(YamlLayer {
        data,
        origin,
        path: path.to_string(),
    })
}

/// Reads a flat top-level YAML file into a layer. An empty path or a
/// missing file yields an empty layer.
#[cfg(not(target_arch = "wasm32"))]
pub fn load_yaml_layer(path: &str, origin: Origin) -> Result<YamlLayer, super::Error> {
    let empty = YamlLayer {
        data: Map::new(),
        origin,
        path: path.to_string(),
    };
    if path.is_empty() {
        return Ok(empty);
    }
    match std::fs::read_to_string(path) {
        Ok(src) => yaml_layer_from_str(&src, origin, path),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(empty),
        Err(e) => Err(super::error::io_error(
            "open",
            std::path::Path::new(path),
            &e,
        )),
    }
}

/// Built-in fallbacks; the lowest rung.
#[derive(Clone, Debug, Default)]
pub struct DefaultLayer {
    pub defaults: HashMap<String, String>,
}

impl Layer for DefaultLayer {
    fn origin(&self) -> Origin {
        Origin::Default
    }
    fn lookup(&self, key: &str) -> Option<String> {
        self.defaults.get(key).cloned()
    }
    fn detail(&self, _key: &str) -> String {
        "built-in".to_string()
    }
}

/// Walks `layers` in order and returns the first hit. No hit yields an
/// empty value with origin [`Origin::Default`] and no detail.
pub fn resolve_string(key: &str, layers: &[&dyn Layer]) -> Resolved<String> {
    for l in layers {
        if let Some(v) = l.lookup(key) {
            return Resolved {
                key: key.to_string(),
                value: v,
                origin: l.origin(),
                detail: l.detail(key),
            };
        }
    }
    Resolved {
        key: key.to_string(),
        ..Resolved::default()
    }
}
