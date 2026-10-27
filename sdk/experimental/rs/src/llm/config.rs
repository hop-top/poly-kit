//! Configuration inputs and results: llm.yaml, the environment and a
//! secret store as [`Sources`], and the [`ResolvedConfig`] a factory
//! receives. `Debug` never prints a key.

use std::collections::BTreeMap;
use std::fmt;

use crate::config::{Map, Value};
use crate::netpolicy::NetPolicy;

use super::error::LlmError;
use super::uri::Uri;

/// One `providers.<name>` block of llm.yaml.
#[derive(Clone, PartialEq, Default)]
pub struct ProviderBlock {
    pub api_key: String,
    /// The variable holding the key, for a key kept under another name.
    pub api_key_env: String,
    pub base_url: String,
    pub model: String,
    /// Every other key of the block.
    pub extras: BTreeMap<String, Value>,
}

impl fmt::Debug for ProviderBlock {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("ProviderBlock")
            .field("api_key", &redacted(&self.api_key))
            .field("api_key_env", &self.api_key_env)
            .field("base_url", &super::redact_uri(&self.base_url))
            .field("model", &self.model)
            .field("extras", &self.extras.keys().collect::<Vec<_>>())
            .finish()
    }
}

/// `{XDG config}/hop/llm.yaml`: the default URI, provider blocks and the
/// fallback chain.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct ConfigFile {
    pub default: String,
    pub providers: BTreeMap<String, ProviderBlock>,
    pub fallback: Vec<String>,
}

impl ConfigFile {
    /// Parse llm.yaml text. Scalars read as their text, as yaml.v3 does
    /// for string fields.
    pub fn parse(src: &str) -> Result<Self, LlmError> {
        let layer =
            crate::config::yaml_layer_from_str(src, crate::config::Origin::Global, "llm.yaml")
                .map_err(|e| LlmError::invalid(format!("llm: llm.yaml: {e}")))?;
        Ok(Self::from_map(&layer.data))
    }

    fn from_map(m: &Map) -> Self {
        let mut cf = ConfigFile {
            default: m.get("default").map(scalar).unwrap_or_default(),
            ..ConfigFile::default()
        };
        if let Some(Value::Seq(items)) = m.get("fallback") {
            cf.fallback = items.iter().map(scalar).collect();
        }
        if let Some(Value::Map(providers)) = m.get("providers") {
            for (name, block) in providers {
                let Value::Map(block) = block else { continue };
                let mut b = ProviderBlock::default();
                for (k, v) in block {
                    match k.as_str() {
                        "api_key" => b.api_key = scalar(v),
                        "api_key_env" => b.api_key_env = scalar(v),
                        "base_url" => b.base_url = scalar(v),
                        "model" => b.model = scalar(v),
                        _ => {
                            b.extras.insert(k.clone(), v.clone());
                        }
                    }
                }
                cf.providers.insert(name.clone(), b);
            }
        }
        cf
    }

    /// Read `{XDG config}/hop/llm.yaml`. Best effort, as in Go: a missing
    /// or unreadable file is an empty config.
    #[cfg(not(target_arch = "wasm32"))]
    pub fn load() -> Self {
        let path = crate::xdg::raw_config_dir("hop").join("llm.yaml");
        std::fs::read_to_string(path)
            .ok()
            .and_then(|s| Self::parse(&s).ok())
            .unwrap_or_default()
    }

    /// The first of `names` with a block, and its name.
    pub(crate) fn block<'a>(&'a self, names: &[&'a str]) -> Option<(&'a str, &'a ProviderBlock)> {
        names
            .iter()
            .find_map(|n| self.providers.get(*n).map(|b| (*n, b)))
    }
}

fn scalar(v: &Value) -> String {
    match v {
        Value::String(s) => s.clone(),
        Value::Bool(b) => b.to_string(),
        Value::Int(i) => i.to_string(),
        Value::Uint(u) => u.to_string(),
        Value::Float(f) => f.to_string(),
        Value::Null | Value::Seq(_) | Value::Map(_) => String::new(),
    }
}

/// A secret store a key may sit in (keychain, vault). `Err` reports a
/// backend failure; it names the key asked for, never a value.
pub trait SecretStore: Send + Sync {
    fn get(&self, name: &str) -> Result<Option<String>, String>;
}

/// Where key and config resolution reads from. Tests and embedders pass
/// their own environment; [`process_env`] reads the process's.
#[derive(Clone, Copy)]
pub struct Sources<'a> {
    pub file: &'a ConfigFile,
    pub env: &'a (dyn Fn(&str) -> Option<String> + Sync),
    /// `None` skips the store.
    pub store: Option<&'a dyn SecretStore>,
}

impl Sources<'_> {
    pub(crate) fn env(&self, name: &str) -> Option<String> {
        (self.env)(name).filter(|v| !v.is_empty())
    }
}

/// The process environment, for [`Sources::env`].
pub fn process_env(name: &str) -> Option<String> {
    std::env::var(name).ok()
}

/// Resolved provider settings handed to a factory.
#[derive(Clone, PartialEq, Default)]
pub struct ProviderConfig {
    /// Never printed: `Debug` shows whether one is set.
    pub api_key: String,
    pub base_url: String,
    pub model: String,
    /// The URI's query params.
    pub params: BTreeMap<String, String>,
    /// The llm.yaml block's other keys.
    pub extras: BTreeMap<String, Value>,
}

impl fmt::Debug for ProviderConfig {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let params = Uri {
            params: self.params.clone(),
            ..Uri::default()
        };
        f.debug_struct("ProviderConfig")
            .field("api_key", &redacted(&self.api_key))
            .field("base_url", &super::redact_uri(&self.base_url))
            .field("model", &self.model)
            .field("params", &params)
            .field("extras", &self.extras.keys().collect::<Vec<_>>())
            .finish()
    }
}

/// The merged configuration: the parsed URI, provider settings, the
/// fallback chain, and the network policy the adapter must honour.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct ResolvedConfig {
    pub uri: Uri,
    pub provider: ProviderConfig,
    pub fallbacks: Vec<String>,
    pub net_policy: NetPolicy,
    /// Read nothing ambient while the adapter builds and runs (Go
    /// `ResolvedConfig.Hermetic`): no environment variable (the
    /// anthropic adapter's `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`,
    /// `ANTHROPIC_WORKSPACE_ID` and `LLM_BASE_URL`) and no file
    /// (llm.yaml). Only this config counts. Set by
    /// [`ResolveOptions::hermetic`](super::ResolveOptions::hermetic) or
    /// an adapter builder's `hermetic`.
    pub hermetic: bool,
}

fn redacted(v: &str) -> &'static str {
    if v.is_empty() {
        ""
    } else {
        "REDACTED"
    }
}
