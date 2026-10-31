//! Rule model and YAML loading. Go's config.go and the types of
//! policy.go.

use std::collections::HashSet;
use std::fmt;

use serde::Deserialize;

use crate::domain::quote::quote;

use super::error::Error;

/// State machine pre-transition topic.
pub const TOPIC_STATE_PRE_TRANSITIONED: &str = "kit.runtime.state.pre_transitioned";
/// Entity pre-validation topic (raw input).
pub const TOPIC_ENTITY_PRE_VALIDATED: &str = "kit.runtime.entity.pre_validated";
/// Entity pre-persist topic (validated entity).
pub const TOPIC_ENTITY_PRE_PERSISTED: &str = "kit.runtime.entity.pre_persisted";
/// Published when a stage-driven rule denies.
pub const TOPIC_STAGE_VIOLATED: &str = "kit.runtime.stage.violated";

/// The veto-able topics a rule may watch. Also the stage-gated set: a
/// stage-driven denial on any of them emits [`TOPIC_STAGE_VIOLATED`].
pub const VETO_TOPICS: [&str; 3] = [
    TOPIC_STATE_PRE_TRANSITIONED,
    TOPIC_ENTITY_PRE_VALIDATED,
    TOPIC_ENTITY_PRE_PERSISTED,
];

/// The outcome of one rule. Go's `Decision`.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Decision {
    /// The event may proceed.
    Allow,
    /// The event is vetoed.
    Deny,
    /// The rule did not apply.
    Skip,
}

impl Decision {
    /// `allow`, `deny` or `skip`.
    pub fn as_str(self) -> &'static str {
        match self {
            Decision::Allow => "allow",
            Decision::Deny => "deny",
            Decision::Skip => "skip",
        }
    }
}

impl fmt::Display for Decision {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

/// What a rule resolves to. Go's `Effect`.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Effect {
    /// Resolve to allow.
    Allow,
    /// Resolve to deny.
    Deny,
}

/// One declarative rule. Go's `Policy`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Policy {
    /// Unique within a [`Config`].
    pub name: String,
    /// The kit topic this rule matches.
    pub on: String,
    /// The CEL expression.
    pub when: String,
    /// Resolution when `when` is true.
    pub effect: Effect,
    /// Resolution when `when` is false.
    pub otherwise: Effect,
    /// Surfaces in [`PolicyDeniedError`](super::PolicyDeniedError).
    pub message: String,
}

/// A parsed rule file. Go's `Config`.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Config {
    /// The rules, in file order.
    pub policies: Vec<Policy>,
}

#[derive(Deserialize)]
struct RawConfig {
    #[serde(default)]
    policies: Vec<RawPolicy>,
}

// Unknown keys are ignored, as yaml.v3 does by default.
#[derive(Deserialize, Default)]
struct RawPolicy {
    #[serde(default)]
    name: String,
    #[serde(default)]
    on: String,
    #[serde(default)]
    when: String,
    #[serde(default)]
    effect: String,
    #[serde(default)]
    otherwise: String,
    #[serde(default)]
    message: String,
    /// Rejected at parse: sync veto only.
    #[serde(default, rename = "async")]
    is_async: bool,
}

/// Reads and parses a rule file. Go's `LoadConfig`. Not on wasm32,
/// which has no filesystem; use [`parse_config`].
#[cfg(not(target_arch = "wasm32"))]
pub fn load_config(path: impl AsRef<std::path::Path>) -> Result<Config, Error> {
    let path = path.as_ref();
    let data = std::fs::read(path)
        .map_err(|e| Error::new(format!("policy: read {}: {e}", path.display())))?;
    parse_config(&data)
}

/// Parses and validates rule YAML. Go's `ParseConfig`.
///
/// Rejected with Go's messages: a missing or duplicate name, `async`,
/// an `on` that is not one of [`VETO_TOPICS`], a missing `when`, and a
/// missing or invalid `effect` or `otherwise`.
pub fn parse_config(data: &[u8]) -> Result<Config, Error> {
    // An empty document is a config with no rules, as in Go.
    let raw: RawConfig = if data.iter().all(u8::is_ascii_whitespace) {
        RawConfig { policies: vec![] }
    } else {
        serde_norway::from_slice(data)
            .map_err(|e| Error::new(format!("policy: parse yaml: {e}")))?
    };
    let mut cfg = Config {
        policies: Vec::with_capacity(raw.policies.len()),
    };
    let mut seen = HashSet::new();
    for (i, rp) in raw.policies.into_iter().enumerate() {
        if rp.name.is_empty() {
            return Err(Error::new(format!("policy: index {i}: name required")));
        }
        if !seen.insert(rp.name.clone()) {
            return Err(Error::new(format!(
                "policy {}: duplicate name",
                quote(&rp.name)
            )));
        }
        if rp.is_async {
            return Err(Error::new(format!(
                "policy {}: async not supported (sync veto only)",
                quote(&rp.name)
            )));
        }
        if !VETO_TOPICS.contains(&rp.on.as_str()) {
            return Err(Error::new(format!(
                "policy {}: 'on' {} not a veto-able topic (allowed: {})",
                quote(&rp.name),
                quote(&rp.on),
                VETO_TOPICS.join(", ")
            )));
        }
        if rp.when.is_empty() {
            return Err(Error::new(format!(
                "policy {}: 'when' required",
                quote(&rp.name)
            )));
        }
        let effect = parse_effect(&rp.effect, "effect", &rp.name)?;
        let otherwise = parse_effect(&rp.otherwise, "otherwise", &rp.name)?;
        cfg.policies.push(Policy {
            name: rp.name,
            on: rp.on,
            when: rp.when,
            effect,
            otherwise,
            message: rp.message,
        });
    }
    Ok(cfg)
}

fn parse_effect(s: &str, field: &str, name: &str) -> Result<Effect, Error> {
    match s {
        "allow" => Ok(Effect::Allow),
        "deny" => Ok(Effect::Deny),
        "" => Err(Error::new(format!(
            "policy {}: {field} required (allow|deny)",
            quote(name)
        ))),
        other => Err(Error::new(format!(
            "policy {}: {field} {} invalid (want allow|deny)",
            quote(name),
            quote(other)
        ))),
    }
}

/// The shipped stage ruleset (`stage.yaml`), byte-identical to Go's.
pub const STAGE_YAML: &str = include_str!("stage.yaml");

/// [`STAGE_YAML`] parsed: the six default operating-mode rules.
pub fn stage_config() -> Config {
    // The shipped file is checked by the tests.
    parse_config(STAGE_YAML.as_bytes()).expect("shipped stage.yaml parses")
}
