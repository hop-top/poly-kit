//! RouteLLM router configuration, kept in a provider's extras under the
//! `routellm` key of llm.yaml. Environment overrides, highest
//! precedence: `ROUTELLM_BASE_URL`, `ROUTELLM_STRONG_MODEL`,
//! `ROUTELLM_WEAK_MODEL`, `ROUTELLM_ROUTERS` (comma-separated).

use std::collections::BTreeMap;

use serde::Deserialize;
use serde_json::Value;

/// The full RouteLLM router configuration.
#[derive(Debug, Clone, PartialEq, Deserialize)]
#[serde(default)]
pub struct RouterConfig {
    pub base_url: String,
    pub grpc_port: u16,
    pub strong_model: String,
    pub weak_model: String,
    pub routers: Vec<String>,
    pub router_config: BTreeMap<String, Value>,
    pub eva: EvaConfig,
    pub autostart: bool,
    pub pid_file: String,
}

/// Evaluation and contract enforcement settings.
#[derive(Debug, Clone, PartialEq, Eq, Default, Deserialize)]
#[serde(default)]
pub struct EvaConfig {
    pub contracts: Vec<String>,
    pub enforce: bool,
}

impl Default for RouterConfig {
    /// Go's `DefaultRouterConfig`.
    fn default() -> Self {
        RouterConfig {
            base_url: "http://localhost:6060".into(),
            grpc_port: 6061,
            strong_model: String::new(),
            weak_model: String::new(),
            routers: Vec::new(),
            router_config: BTreeMap::new(),
            eva: EvaConfig::default(),
            autostart: false,
            pid_file: String::new(),
        }
    }
}

impl RouterConfig {
    /// Extract and validate the configuration from a provider's extras.
    /// Missing keys take the defaults; `env` overrides them all.
    pub fn parse(
        extras: &BTreeMap<String, crate::config::Value>,
        env: impl Fn(&str) -> Option<String>,
    ) -> Result<Self, String> {
        let mut cfg = match extras.get("routellm") {
            None => RouterConfig::default(),
            Some(v @ crate::config::Value::Map(_)) => {
                let json = serde_json::to_value(v).map_err(|e| format!("routellm: {e}"))?;
                serde_json::from_value(json).map_err(|e| format!("routellm: {e}"))?
            }
            Some(other) => {
                return Err(format!(
                    "routellm: expected map, got {}",
                    type_name(&serde_json::to_value(other).unwrap_or(Value::Null))
                ))
            }
        };
        let get = |n: &str| env(n).filter(|v| !v.is_empty());
        if let Some(v) = get("ROUTELLM_BASE_URL") {
            cfg.base_url = v;
        }
        if let Some(v) = get("ROUTELLM_STRONG_MODEL") {
            cfg.strong_model = v;
        }
        if let Some(v) = get("ROUTELLM_WEAK_MODEL") {
            cfg.weak_model = v;
        }
        if let Some(v) = get("ROUTELLM_ROUTERS") {
            cfg.routers = v
                .split(',')
                .map(str::trim)
                .filter(|s| !s.is_empty())
                .map(str::to_string)
                .collect();
        }
        Ok(cfg)
    }
}

fn type_name(v: &Value) -> &'static str {
    match v {
        Value::Null => "null",
        Value::Bool(_) => "bool",
        Value::Number(_) => "number",
        Value::String(_) => "string",
        Value::Array(_) => "array",
        Value::Object(_) => "map",
    }
}
