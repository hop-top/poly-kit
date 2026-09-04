//! Topic type, wildcard matching, and the two validators.
//!
//! Mirrors the Go canonical runtime (`go/runtime/bus/event.go`,
//! `topics.go`, `validate.go`). The notation is
//! `[Source].[Category].[Object].[Action]`:
//!
//! - **Source** — system or domain originating the event (`crm`, `app`)
//! - **Category** — logical grouping within the source (`sales`)
//! - **Object** — entity type that changed (`deal`)
//! - **Action** — what happened, past tense (`created`)
//!
//! Two validators exist, deliberately, with different strictness:
//!
//! - [`validate`] — the *published-topic* contract. 4 segments matching
//!   `^[a-z][a-z0-9_]*$`, total length <= 128, wildcards rejected. Does
//!   not check verb tense. This is what [`crate::bus::Bus::publish`]
//!   enforces.
//! - [`validate_topic`] — the *construction-time* convention, additionally
//!   requiring a past-tense action segment and permitting a leading digit
//!   or underscore in a segment. Used by [`prefix_topics`] so misconfigured
//!   topic maps fail loudly during adopter wiring.

use std::collections::BTreeMap;
use std::fmt;

use serde::{Deserialize, Serialize};

/// Maximum total length of a published topic.
const MAX_TOPIC_LEN: usize = 128;

/// Action segments that are valid past tense but do not match the simple
/// "ends in `ed`" heuristic, plus a few present-participle forms used for
/// in-flight signals.
///
/// Adding to this list is the documented way to extend [`validate_topic`]
/// when a new action verb does not fit the heuristic.
pub const PAST_TENSE_WHITELIST: &[&str] = &[
    "started",
    "ended",
    "succeeded",
    "failed",
    "canceled",
    "snoozed",
    "received",
    "sent",
    "applied",
    "selected",
    "evaluated",
    "installed",
    "downloaded",
    "released",
    "tripped",
    "opened",
    "closed",
    "half_opened",
    "paid",
    "made",
    "built",
    "read",
    "set",
    "put",
    "hit",
    "lost",
    "found",
    "won",
];

/// A dot-separated event path following `[Source].[Category].[Object].[Action]`.
///
/// Examples:
///
/// - `crm.sales.deal.created`
/// - `app.support.ticket.escalated`
/// - `billing.finance.invoice.paid`
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
#[serde(transparent)]
pub struct Topic(pub String);

impl Topic {
    /// Borrows the topic as a string slice.
    pub fn as_str(&self) -> &str {
        &self.0
    }

    /// Reports whether `pattern` matches this topic.
    ///
    /// Rules:
    ///
    /// - Exact match: `llm.request` matches `llm.request`
    /// - `*` matches exactly one segment: `llm.*` matches `llm.request`
    ///   but not `llm.request.start`
    /// - `#` matches zero or more trailing segments: `llm.#` matches
    ///   `llm`, `llm.request`, and `llm.request.start`
    ///
    /// Per MQTT convention `#` must be the final segment; a `#` anywhere
    /// else never matches.
    pub fn matches(&self, pattern: &str) -> bool {
        let topic: Vec<&str> = self.0.split('.').collect();
        let pattern: Vec<&str> = pattern.split('.').collect();
        match_parts(&topic, &pattern)
    }
}

fn match_parts(topic: &[&str], pattern: &[&str]) -> bool {
    let mut ti = 0usize;
    let mut pi = 0usize;
    while pi < pattern.len() {
        if pattern[pi] == "#" {
            // Per MQTT convention, # must be the last segment.
            return pi == pattern.len() - 1;
        }
        if ti >= topic.len() {
            return false;
        }
        if pattern[pi] != "*" && pattern[pi] != topic[ti] {
            return false;
        }
        ti += 1;
        pi += 1;
    }
    ti == topic.len()
}

impl fmt::Display for Topic {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl From<&str> for Topic {
    fn from(s: &str) -> Self {
        Topic(s.to_string())
    }
}

impl From<String> for Topic {
    fn from(s: String) -> Self {
        Topic(s)
    }
}

/// Maps an action key (e.g. `created`) to a fully-qualified [`Topic`]
/// (e.g. `kit.runtime.entity.created`).
///
/// Modules that publish several related events expose a `TopicMap` so
/// adopters can override individual entries. Ordered so iteration is
/// deterministic.
pub type TopicMap = BTreeMap<String, Topic>;

/// Error returned by [`validate`], carrying the offending topic and the
/// reason it failed.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct InvalidTopicError {
    pub topic: Topic,
    pub reason: String,
}

impl fmt::Display for InvalidTopicError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "bus: invalid topic: {:?} ({})",
            self.topic.0, self.reason
        )
    }
}

impl std::error::Error for InvalidTopicError {}

/// Checks that `t` conforms to the published-topic naming contract:
///
/// - exactly 4 segments separated by `.`
/// - each segment matches `^[a-z][a-z0-9_]*$`
/// - total length <= 128 characters
/// - wildcards (`*`, `#`) are NEVER permitted in published topics
///
/// This is for published topics only. It does not affect
/// [`Topic::matches`] semantics; subscribe patterns retain wildcards.
pub fn validate(t: &Topic) -> Result<(), InvalidTopicError> {
    let s = t.as_str();
    let invalid = |reason: String| InvalidTopicError {
        topic: t.clone(),
        reason,
    };
    if s.is_empty() {
        return Err(invalid("empty topic".to_string()));
    }
    if s.len() > MAX_TOPIC_LEN {
        return Err(invalid(format!(
            "length {} exceeds max {MAX_TOPIC_LEN}",
            s.len()
        )));
    }
    let parts: Vec<&str> = s.split('.').collect();
    if parts.len() != 4 {
        return Err(invalid(format!(
            "expected 4 segments [Source].[Category].[Object].[Action], got {}",
            parts.len()
        )));
    }
    for (i, p) in parts.iter().enumerate() {
        if let Err(e) = validate_segment(p) {
            return Err(invalid(format!("segment {} ({:?}): {}", i + 1, p, e)));
        }
    }
    Ok(())
}

/// Enforces `^[a-z][a-z0-9_]*$` on a single segment, rejecting wildcards
/// explicitly so the error message is friendly.
fn validate_segment(seg: &str) -> Result<(), String> {
    if seg.is_empty() {
        return Err("empty segment".to_string());
    }
    if seg == "*" || seg == "#" {
        return Err("wildcards are not allowed in published topics".to_string());
    }
    for (i, r) in seg.chars().enumerate() {
        match r {
            'a'..='z' => {}
            '0'..='9' | '_' => {
                if i == 0 {
                    return Err("must start with a lowercase letter".to_string());
                }
            }
            other => {
                return Err(format!(
                    "invalid character {other:?} (allowed: a-z, 0-9, _)"
                ));
            }
        }
    }
    Ok(())
}

/// Returns `Ok(())` when `t` conforms to the 4-segment past-tense
/// convention:
///
/// - exactly 4 dot-separated segments: `source.category.object.action`
/// - all segments lowercase ASCII letters, digits, or underscores
/// - no empty segments
/// - action segment is past tense: ends in `ed`, or appears in
///   [`PAST_TENSE_WHITELIST`] (irregular forms plus a few participles)
///
/// Multi-word actions use snake_case (`pre_transitioned`, `half_opened`).
/// The `ed`-ending check uses the whole final segment, so
/// `pre_transitioned` passes naturally.
///
/// Intentionally strict at construction time so misconfigured topic
/// prefixes fail loudly during adopter wiring, not at runtime when
/// subscribers silently fail to receive expected events.
pub fn validate_topic(t: &Topic) -> Result<(), String> {
    let s = t.as_str();
    if s.is_empty() {
        return Err("topic is empty (expected source.category.object.action)".to_string());
    }
    let parts: Vec<&str> = s.split('.').collect();
    if parts.len() != 4 {
        return Err(format!(
            "topic {s:?} has {} segments; expected 4 (source.category.object.action)",
            parts.len()
        ));
    }
    for (i, seg) in parts.iter().enumerate() {
        if seg.is_empty() {
            return Err(format!("topic {s:?} has empty segment at position {i}"));
        }
        if !valid_segment(seg) {
            return Err(format!(
                "topic {s:?} segment {seg:?} must be lowercase letters, digits, or underscores"
            ));
        }
    }
    let action = parts[3];
    if !is_past_tense(action) {
        return Err(format!(
            "topic {s:?} action segment {action:?} is not past-tense \
             (e.g. \"started\", \"created\"); see bus::PAST_TENSE_WHITELIST"
        ));
    }
    Ok(())
}

/// Builds a [`TopicMap`] from a 3-segment prefix and a slice of past-tense
/// action segments. Each composed topic is `<prefix>.<action>`, validated
/// via [`validate_topic`].
///
/// ```
/// # use hop_top_kit::bus::prefix_topics;
/// let (map, err) = prefix_topics("wsm.runtime.workspace", &["created", "updated"]);
/// assert!(err.is_none());
/// assert_eq!(map["created"].as_str(), "wsm.runtime.workspace.created");
/// ```
///
/// Returns the partial map alongside the first validation error
/// encountered. Callers that want strict-or-empty semantics should treat
/// any `Some(err)` as fatal. Mirrors the Go signature, which returns the
/// partial map so callers can see which actions did validate.
pub fn prefix_topics(prefix: &str, actions: &[&str]) -> (TopicMap, Option<String>) {
    let empty = TopicMap::new();
    if prefix.is_empty() {
        return (empty, Some("prefix is empty".to_string()));
    }
    if prefix.ends_with('.') {
        return (
            empty,
            Some(format!("prefix {prefix:?} must not end with '.'")),
        );
    }
    let parts: Vec<&str> = prefix.split('.').collect();
    if parts.len() != 3 {
        return (
            empty,
            Some(format!(
                "prefix {prefix:?} has {} segments; expected 3 (source.category.object)",
                parts.len()
            )),
        );
    }
    for (i, seg) in parts.iter().enumerate() {
        if seg.is_empty() {
            return (
                empty,
                Some(format!(
                    "prefix {prefix:?} has empty segment at position {i}"
                )),
            );
        }
        if !valid_segment(seg) {
            return (
                empty,
                Some(format!(
                    "prefix {prefix:?} segment {seg:?} must be lowercase letters, digits, or underscores"
                )),
            );
        }
    }

    let mut out = TopicMap::new();
    for a in actions {
        let topic = Topic(format!("{prefix}.{a}"));
        if let Err(e) = validate_topic(&topic) {
            return (
                out,
                Some(format!("prefix_topics({prefix:?}, {actions:?}): {e}")),
            );
        }
        out.insert((*a).to_string(), topic);
    }
    (out, None)
}

/// Reports whether `seg` consists only of lowercase ASCII letters, digits,
/// and underscores, and is non-empty.
fn valid_segment(seg: &str) -> bool {
    !seg.is_empty()
        && seg
            .chars()
            .all(|r| r.is_ascii_lowercase() || r.is_ascii_digit() || r == '_')
}

/// Reports whether `action` is a recognized past-tense verb per the
/// heuristic plus the whitelist.
fn is_past_tense(action: &str) -> bool {
    action.ends_with("ed") || PAST_TENSE_WHITELIST.contains(&action)
}
