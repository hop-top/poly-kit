//! Tool-name rules and the reversible sanitizer, a port of Go
//! `llm/toolnames.go`. Normative spec: `contracts/llm-v1/tool-names.md`.
//!
//! Providers restrict tool names; callers use ids such as `pane.open`.
//! A tool-calling adapter builds one [`ToolNames`] per request: it
//! checks the request's [`ToolDef`] names, sends names the provider
//! accepts and maps every returned tool call back to the caller's name.

use std::collections::{BTreeMap, HashMap};

use sha2::{Digest, Sha256};

use super::error::{LlmError, ToolNameReason};
use super::types::{ToolCall, ToolDef};

/// The provider param that selects the [`ToolNameMode`] of a
/// tool-calling adapter, as in `anthropic://model?tool_names=strict`.
pub const TOOL_NAMES_PARAM: &str = "tool_names";

/// How an adapter treats a [`ToolDef`] name its provider does not
/// accept.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum ToolNameMode {
    /// The default: send the name as [`ToolNameRule::sanitize`] rewrites
    /// it and map it back to the original in every returned tool call.
    #[default]
    Sanitize,
    /// Send names as given; a name the provider does not accept fails
    /// the request with [`LlmError::ToolName`] (reason
    /// [`ToolNameReason::Invalid`]) before any network call.
    Strict,
}

impl ToolNameMode {
    pub fn as_str(self) -> &'static str {
        match self {
            ToolNameMode::Sanitize => "sanitize",
            ToolNameMode::Strict => "strict",
        }
    }

    /// Reads [`TOOL_NAMES_PARAM`] from provider params; absent means
    /// [`ToolNameMode::Sanitize`]. Any value but `sanitize` or `strict`
    /// (surrounding whitespace ignored) is [`LlmError::Invalid`].
    pub fn from_params(params: &BTreeMap<String, String>) -> Result<Self, LlmError> {
        let Some(v) = params.get(TOOL_NAMES_PARAM) else {
            return Ok(ToolNameMode::Sanitize);
        };
        match v.trim() {
            "sanitize" => Ok(ToolNameMode::Sanitize),
            "strict" => Ok(ToolNameMode::Strict),
            _ => Err(LlmError::invalid(format!(
                "{TOOL_NAMES_PARAM} must be \"sanitize\" or \"strict\", got {v:?}"
            ))),
        }
    }
}

/// A provider's rule for tool names: 1 to `max_len` bytes of ASCII
/// letters, digits, `_` and `-`, plus the bytes in `extra`; with
/// `lead_letter`, the first byte must be a letter or `_`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ToolNameRule {
    /// Names the rule in errors.
    pub provider: &'static str,
    pub max_len: usize,
    pub extra: &'static str,
    pub lead_letter: bool,
}

/// Anthropic Messages API: `^[a-zA-Z0-9_-]{1,128}$`.
pub const ANTHROPIC_TOOL_NAMES: ToolNameRule = ToolNameRule {
    provider: "anthropic",
    max_len: 128,
    extra: "",
    lead_letter: false,
};

/// OpenAI Chat Completions and the compatible servers:
/// `^[a-zA-Z0-9_-]{1,64}$`.
pub const OPENAI_TOOL_NAMES: ToolNameRule = ToolNameRule {
    provider: "openai",
    max_len: 64,
    extra: "",
    lead_letter: false,
};

/// Gemini `FunctionDeclaration.name`: `^[a-zA-Z_][a-zA-Z0-9_.:-]{0,127}$`.
pub const GEMINI_TOOL_NAMES: ToolNameRule = ToolNameRule {
    provider: "gemini",
    max_len: 128,
    extra: ".:",
    lead_letter: true,
};

/// Hex digits of the name's SHA-256 a shortened name ends with, after
/// a `_`.
const HASH_LEN: usize = 8;

impl ToolNameRule {
    fn allowed(&self, c: u8) -> bool {
        c.is_ascii_alphanumeric() || c == b'_' || c == b'-' || self.extra.as_bytes().contains(&c)
    }

    fn lead_ok(&self, c: u8) -> bool {
        !self.lead_letter || c == b'_' || c.is_ascii_alphabetic()
    }

    fn error(&self, reason: ToolNameReason, name: &str, other: &str) -> LlmError {
        LlmError::ToolName {
            provider: self.provider.to_string(),
            reason,
            name: name.to_string(),
            other: other.to_string(),
        }
    }

    /// Whether the provider accepts `name` as is.
    pub fn valid(&self, name: &str) -> bool {
        let b = name.as_bytes();
        !b.is_empty()
            && b.len() <= self.max_len
            && self.lead_ok(b[0])
            && b.iter().all(|&c| self.allowed(c))
    }

    /// `name` as the provider accepts it. A valid name comes back
    /// unchanged. Otherwise, over the UTF-8 bytes of `name`:
    ///
    /// 1. every maximal run of bytes the rule does not allow becomes one `_`;
    /// 2. with `lead_letter`, a first byte that is not a letter or `_`
    ///    gets a `_` in front;
    /// 3. a result longer than `max_len`, or holding no ASCII letter or
    ///    digit, keeps its first `max_len - 9` bytes (all of it when
    ///    shorter) and gains `_` and the first 8 lowercase hex digits of
    ///    SHA-256(`name`).
    ///
    /// An empty name is [`LlmError::ToolName`] (reason
    /// [`ToolNameReason::Empty`]); the result is never empty.
    pub fn sanitize(&self, name: &str) -> Result<String, LlmError> {
        if name.is_empty() {
            return Err(self.error(ToolNameReason::Empty, name, ""));
        }
        if self.valid(name) {
            return Ok(name.to_string());
        }
        let mut out = String::with_capacity(name.len() + 1);
        let mut run = false;
        for &c in name.as_bytes() {
            if self.allowed(c) {
                out.push(char::from(c));
                run = false;
            } else {
                if !run {
                    out.push('_');
                }
                run = true;
            }
        }
        if !self.lead_ok(out.as_bytes()[0]) {
            out.insert(0, '_');
        }
        if out.len() <= self.max_len && out.bytes().any(|c| c.is_ascii_alphanumeric()) {
            return Ok(out);
        }
        // Every byte of `out` is ASCII: truncating at any index is safe.
        out.truncate(out.len().min(self.max_len - HASH_LEN - 1));
        out.push('_');
        out.push_str(&hash_prefix(name));
        Ok(out)
    }
}

/// The first [`HASH_LEN`] lowercase hex digits of SHA-256(`name`).
fn hash_prefix(name: &str) -> String {
    let sum = Sha256::digest(name.as_bytes());
    sum.iter()
        .take(HASH_LEN / 2)
        .map(|b| format!("{b:02x}"))
        .collect()
}

/// One request's tool names mapped to the names sent to the provider
/// (wire names) and back. Adapters build one per request, tools or not,
/// so replayed history is mapped too.
#[derive(Debug, Clone)]
pub struct ToolNames {
    rule: ToolNameRule,
    mode: ToolNameMode,
    to_wire: HashMap<String, String>,
    to_orig: HashMap<String, String>,
}

impl ToolNames {
    /// Checks `defs` against `rule` and builds their mapping. The first
    /// failing def, in order, fails the request with
    /// [`LlmError::ToolName`]:
    ///
    /// - [`Empty`](ToolNameReason::Empty): an empty name;
    /// - [`Invalid`](ToolNameReason::Invalid): in
    ///   [`ToolNameMode::Strict`], a name `rule` does not accept;
    /// - [`Duplicate`](ToolNameReason::Duplicate): a name an earlier def
    ///   already has;
    /// - [`Collision`](ToolNameReason::Collision): a name whose wire name
    ///   an earlier def already has (`other` names that def).
    pub fn new(rule: ToolNameRule, mode: ToolNameMode, defs: &[ToolDef]) -> Result<Self, LlmError> {
        let mut t = ToolNames {
            rule,
            mode,
            to_wire: HashMap::with_capacity(defs.len()),
            to_orig: HashMap::with_capacity(defs.len()),
        };
        for d in defs {
            let name = d.name.as_str();
            let wire = if name.is_empty() {
                return Err(rule.error(ToolNameReason::Empty, name, ""));
            } else if mode == ToolNameMode::Strict {
                if !rule.valid(name) {
                    return Err(rule.error(ToolNameReason::Invalid, name, ""));
                }
                name.to_string()
            } else {
                rule.sanitize(name)?
            };
            if t.to_wire.contains_key(name) {
                return Err(rule.error(ToolNameReason::Duplicate, name, ""));
            }
            if let Some(other) = t.to_orig.get(&wire) {
                return Err(rule.error(ToolNameReason::Collision, name, other));
            }
            t.to_wire.insert(name.to_string(), wire.clone());
            t.to_orig.insert(wire, name.to_string());
        }
        Ok(t)
    }

    /// The rule names are checked against.
    pub fn rule(&self) -> ToolNameRule {
        self.rule
    }

    /// The mode names are mapped in.
    pub fn mode(&self) -> ToolNameMode {
        self.mode
    }

    /// The name to send for `name`: a def's wire name, else, as for a
    /// call replayed from history, `name` sanitized
    /// ([`ToolNameMode::Sanitize`]) or unchanged ([`ToolNameMode::Strict`],
    /// or a name [`ToolNameRule::sanitize`] refuses).
    pub fn wire(&self, name: &str) -> String {
        if let Some(w) = self.to_wire.get(name) {
            return w.clone();
        }
        if self.mode == ToolNameMode::Strict {
            return name.to_string();
        }
        self.rule
            .sanitize(name)
            .unwrap_or_else(|_| name.to_string())
    }

    /// The def name a wire name the provider returned stands for; a name
    /// no def maps to comes back unchanged.
    pub fn original<'a>(&'a self, wire: &'a str) -> &'a str {
        self.to_orig.get(wire).map_or(wire, String::as_str)
    }

    /// Replaces each call's name with the original it stands for.
    pub fn restore_calls(&self, calls: &mut [ToolCall]) {
        for c in calls {
            if let Some(o) = self.to_orig.get(&c.name) {
                c.name = o.clone();
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn params(v: &str) -> BTreeMap<String, String> {
        BTreeMap::from([(TOOL_NAMES_PARAM.to_string(), v.to_string())])
    }

    #[test]
    fn mode_from_params() {
        let mode = |v| ToolNameMode::from_params(&params(v));
        assert_eq!(
            ToolNameMode::from_params(&BTreeMap::new()).unwrap(),
            ToolNameMode::Sanitize
        );
        assert_eq!(mode(" strict ").unwrap(), ToolNameMode::Strict);
        assert_eq!(mode("sanitize").unwrap(), ToolNameMode::Sanitize);
        let e = mode("loose").unwrap_err();
        assert_eq!(
            e.to_string(),
            r#"tool_names must be "sanitize" or "strict", got "loose""#
        );
        assert!(mode("").is_err());
    }

    #[test]
    fn tool_name_errors_are_not_fallbackable() {
        let defs = [ToolDef::named("pane.open"), ToolDef::named("pane:open")];
        let e = ToolNames::new(ANTHROPIC_TOOL_NAMES, ToolNameMode::Sanitize, &defs).unwrap_err();
        assert!(!e.is_fallbackable());
        assert_eq!(
            e.to_string(),
            r#"tool names "pane.open" and "pane:open" map to the same provider name (provider "anthropic")"#
        );
    }

    #[test]
    fn restore_calls_maps_known_names_only() {
        let names = ToolNames::new(
            OPENAI_TOOL_NAMES,
            ToolNameMode::Sanitize,
            &[ToolDef::named("a.b")],
        )
        .unwrap();
        let mut calls = vec![
            ToolCall {
                name: "a_b".into(),
                ..ToolCall::default()
            },
            ToolCall {
                name: "c.d".into(),
                ..ToolCall::default()
            },
        ];
        names.restore_calls(&mut calls);
        assert_eq!(calls[0].name, "a.b");
        assert_eq!(calls[1].name, "c.d");
    }
}
