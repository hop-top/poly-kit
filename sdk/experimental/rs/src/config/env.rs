//! Environment and defaults as a key/value view.
//!
//! Go wires these into a `*viper.Viper` (`BindEnv`, `SeedDefaults`,
//! `SyncToViper`, `Options.Viper`). The port has no viper; [`EnvLookup`]
//! keeps viper's lookup behaviour for the pieces Go uses: explicit sets
//! win, then the automatic `<PREFIX>_<KEY>` variable, then explicit
//! bindings, then seeded defaults. Empty variables count as unset and
//! keys are case-insensitive, as in viper. It implements the bus's
//! [`ConfigGetter`](crate::bus::ConfigGetter).

use std::collections::BTreeMap;
use std::fmt;

use serde::Serialize;

use super::precedence::{process_env, EnvFn};
use super::yaml::{de::from_node, ser::to_node};
use super::{Error, Options, Value};

/// Maps a dotted key to an explicit environment variable, for names
/// that do not follow `<PREFIX>_<KEY>`.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct EnvBind {
    /// Dotted key, e.g. `auth.token`.
    pub key: String,
    /// Variable name, e.g. `GH_TOKEN`.
    pub env: String,
}

/// Viper-style key/value view over the environment and defaults.
#[derive(Clone, Default)]
pub struct EnvLookup {
    prefix: String,
    automatic: bool,
    binds: BTreeMap<String, Vec<String>>,
    defaults: BTreeMap<String, Value>,
    sets: BTreeMap<String, Value>,
    env: Option<EnvFn>,
}

impl fmt::Debug for EnvLookup {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("EnvLookup")
            .field("prefix", &self.prefix)
            .field("automatic", &self.automatic)
            .field("binds", &self.binds)
            .field("defaults", &self.defaults)
            .field("sets", &self.sets)
            .finish_non_exhaustive()
    }
}

/// Go `BindEnv`: automatic `<PREFIX>_<KEY>` mapping (`.` → `_`), plus
/// explicit bindings. An empty prefix reads `<KEY>` unprefixed.
pub fn bind_env(prefix: &str, explicit: &[EnvBind]) -> Result<EnvLookup, Error> {
    let mut l = EnvLookup {
        prefix: prefix.to_string(),
        automatic: true,
        ..EnvLookup::default()
    };
    for b in explicit {
        if b.key.is_empty() || b.env.is_empty() {
            return Err(Error::invalid(format!(
                "env bind: empty Key or Env ({{Key:{} Env:{}}})",
                b.key, b.env
            )));
        }
        l.binds
            .entry(b.key.to_lowercase())
            .or_default()
            .push(b.env.clone());
    }
    Ok(l)
}

impl Options {
    /// The [`EnvLookup`] these options describe: env bindings when
    /// `env_prefix` or `env_binds` is set, seeded with the defaults.
    /// Like Go's viper wiring, file values are not in it; read them
    /// from the loaded struct, or [`EnvLookup::sync`] it.
    pub fn env_lookup(&self) -> Result<EnvLookup, Error> {
        let mut l = if !self.env_prefix.is_empty() || !self.env_binds.is_empty() {
            bind_env(&self.env_prefix, &self.env_binds)?
        } else {
            EnvLookup::default()
        };
        if let Some(d) = &self.defaults {
            let v: Value = from_node(&d.0)?;
            flatten_into(&mut l.defaults, "", v);
        }
        Ok(l)
    }
}

impl EnvLookup {
    /// Reads through `env` instead of the process environment.
    pub fn with_env(mut self, env: EnvFn) -> EnvLookup {
        self.env = Some(env);
        self
    }

    /// Go `SeedDefaults`: registers each leaf of `defaults` under its
    /// dotted key at the lowest precedence.
    pub fn seed_defaults<T: Serialize>(&mut self, defaults: &T) -> Result<(), Error> {
        let v = flatten_src(defaults).map_err(|e| e.wrap("seed defaults"))?;
        flatten_into(&mut self.defaults, "", v);
        Ok(())
    }

    /// Go `SyncToViper`: writes `src`'s leaves at the highest
    /// precedence, so the view mirrors a loaded struct.
    pub fn sync<T: Serialize>(&mut self, src: &T) -> Result<(), Error> {
        let v = flatten_src(src).map_err(|e| e.wrap("sync to viper"))?;
        flatten_into(&mut self.sets, "", v);
        Ok(())
    }

    /// Sets one key at the highest precedence (viper `Set`).
    pub fn set(&mut self, key: &str, v: Value) {
        self.sets.insert(key.to_lowercase(), v);
    }

    /// The value for `key` (case-insensitive), by viper's order.
    pub fn get_value(&self, key: &str) -> Option<Value> {
        let k = key.to_lowercase();
        if let Some(v) = self.sets.get(&k) {
            return Some(v.clone());
        }
        if self.automatic {
            let name = if self.prefix.is_empty() {
                k.clone()
            } else {
                format!("{}_{k}", self.prefix)
            };
            if let Some(v) = self.read_env(&name.to_uppercase().replace('.', "_")) {
                return Some(Value::String(v));
            }
        }
        if let Some(names) = self.binds.get(&k) {
            for n in names {
                if let Some(v) = self.read_env(&n.replace('.', "_")) {
                    return Some(Value::String(v));
                }
            }
        }
        self.defaults.get(&k).cloned()
    }

    /// viper `GetString`: the value cast to a string, `""` when absent
    /// or not a scalar.
    pub fn get_string(&self, key: &str) -> String {
        self.get_value(key)
            .map(|v| cast_string(&v))
            .unwrap_or_default()
    }

    fn read_env(&self, name: &str) -> Option<String> {
        let v = match &self.env {
            Some(f) => f(name),
            None => process_env(name),
        }?;
        (!v.is_empty()).then_some(v)
    }
}

/// spf13/cast `ToString`: floats without exponent, non-scalars empty.
pub(crate) fn cast_string(v: &Value) -> String {
    match v {
        Value::String(s) => s.clone(),
        Value::Bool(b) => b.to_string(),
        Value::Int(i) => i.to_string(),
        Value::Uint(u) => u.to_string(),
        Value::Float(f) => f.to_string(),
        Value::Null | Value::Seq(_) | Value::Map(_) => String::new(),
    }
}

fn flatten_src<T: Serialize>(src: &T) -> Result<Value, Error> {
    let node = to_node(src)?;
    Ok(from_node(&node)?)
}

/// Mappings flatten to dotted keys; scalars and lists are leaves.
fn flatten_into(flat: &mut BTreeMap<String, Value>, prefix: &str, v: Value) {
    match v {
        Value::Map(m) => {
            for (k, child) in m {
                let key = if prefix.is_empty() {
                    k
                } else {
                    format!("{prefix}.{k}")
                };
                flatten_into(flat, &key, child);
            }
        }
        leaf if !prefix.is_empty() => {
            flat.insert(prefix.to_lowercase(), leaf);
        }
        _ => {}
    }
}
