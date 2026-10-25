//! Wire primitives: offset tokens, stream ids, patterns and scopes, and
//! the JSON member helpers the frame decoders share.

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};

use super::error::{StreamError, CODE_BAD_REQUEST, CODE_INVALID_PATTERN, CODE_INVALID_SCOPE};
use crate::bus::quote::quote;

/// Frame members: a decoded JSON object.
pub type Members = Map<String, Value>;

/// Parses an offset token: decimal digits, no sign, no leading zeros, at
/// most 2^64-1.
pub fn parse_offset(s: &str) -> Option<u64> {
    let b = s.as_bytes();
    if b.is_empty() || b.len() > 20 || (b.len() > 1 && b[0] == b'0') {
        return None;
    }
    if !b.iter().all(u8::is_ascii_digit) {
        return None;
    }
    s.parse().ok()
}

/// Renders an offset as its decimal token.
pub fn format_offset(v: u64) -> String {
    v.to_string()
}

/// Parses a member that must be an offset token in a JSON string. An
/// absent member, JSON numbers and every other type are refused.
pub fn parse_offset_token(raw: Option<&Value>) -> Option<u64> {
    match raw {
        Some(Value::String(s)) => parse_offset(s),
        _ => None,
    }
}

pub(crate) const MAX_PATTERN_BYTES: usize = 256;
pub(crate) const MAX_REQUEST_ID: usize = 64;
pub(crate) const MAX_STREAM_ID_BYTES: usize = 128;

/// Checks a subscribe pattern: non-empty, at most 256 bytes, non-empty
/// segments, wildcards only as whole segments, `#` last.
pub fn validate_pattern(p: &str) -> Result<(), StreamError> {
    if p.is_empty() || p.len() > MAX_PATTERN_BYTES {
        return Err(StreamError::new(
            CODE_INVALID_PATTERN,
            format!("pattern must be 1-{MAX_PATTERN_BYTES} bytes"),
        ));
    }
    let segs: Vec<&str> = p.split('.').collect();
    for (i, s) in segs.iter().enumerate() {
        let n = i + 1;
        let msg = if s.is_empty() {
            format!("segment {n} is empty")
        } else if *s == "#" && i != segs.len() - 1 {
            format!("segment {n}: {} must be the last segment", quote("#"))
        } else if *s != "*" && *s != "#" && s.contains(['*', '#']) {
            format!("segment {n}: wildcards must be whole segments")
        } else {
            continue;
        };
        return Err(StreamError::new(CODE_INVALID_PATTERN, msg));
    }
    Ok(())
}

/// Reports whether `s` is a usable stream id: 1-128 printable ASCII
/// bytes.
pub fn valid_stream_id(s: &str) -> bool {
    !s.is_empty() && s.len() <= MAX_STREAM_ID_BYTES && s.bytes().all(|b| (0x20..=0x7e).contains(&b))
}

/// Limits a link to some workspaces. Events without a workspace id always
/// pass; an empty list passes only those.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Scope {
    /// The workspaces whose events pass.
    pub workspaces: Vec<String>,
}

impl Scope {
    /// Reports whether an event of `workspace` passes. `None` is no scope
    /// (all workspaces).
    pub fn allows(scope: Option<&Scope>, workspace: &str) -> bool {
        match scope {
            None => true,
            Some(_) if workspace.is_empty() => true,
            Some(s) => s.workspaces.iter().any(|w| w == workspace),
        }
    }
}

/// Reports whether a member is absent or JSON null.
pub(crate) fn is_null(raw: Option<&Value>) -> bool {
    matches!(raw, None | Some(Value::Null))
}

/// Returns a string member, `None` when absent or not a string.
pub(crate) fn member_str<'a>(m: &'a Members, key: &str) -> Option<&'a str> {
    m.get(key).and_then(Value::as_str)
}

/// Decodes `raw` as an array of strings.
fn strings(raw: &Value) -> Option<Vec<String>> {
    raw.as_array()?
        .iter()
        .map(|v| v.as_str().map(str::to_string))
        .collect()
}

/// Decodes a scope member: an object whose `workspaces` is an array of
/// strings.
pub(crate) fn parse_scope(raw: &Value) -> Result<Scope, StreamError> {
    let Some(m) = raw.as_object() else {
        return Err(StreamError::new(
            CODE_INVALID_SCOPE,
            "scope must be an object",
        ));
    };
    let ws = match m.get("workspaces") {
        Some(v) if !v.is_null() => strings(v),
        _ => None,
    };
    match ws {
        Some(workspaces) => Ok(Scope { workspaces }),
        None => Err(StreamError::new(
            CODE_INVALID_SCOPE,
            "workspaces must be an array of strings",
        )),
    }
}

/// Decodes a patterns member and validates each pattern. `allow_empty`
/// admits `[]` (a handshake may subscribe to nothing).
pub(crate) fn parse_patterns(raw: &Value, allow_empty: bool) -> Result<Vec<String>, StreamError> {
    let Some(ps) = strings(raw) else {
        return Err(StreamError::new(
            CODE_BAD_REQUEST,
            "patterns must be an array of strings",
        ));
    };
    if ps.is_empty() && !allow_empty {
        return Err(StreamError::new(
            CODE_BAD_REQUEST,
            "patterns must not be empty",
        ));
    }
    for p in &ps {
        validate_pattern(p)?;
    }
    Ok(ps)
}
