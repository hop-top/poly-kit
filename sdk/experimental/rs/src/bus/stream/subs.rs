//! A link's subscription state and the per-link delivery predicate.

use std::collections::BTreeSet;

use super::control::{CtlRequest, RequestType};
use super::error::{StreamError, CODE_LIMIT};
use super::log::LogEntry;
use super::wire::Scope;

/// A link's effective subscription, as an authorizer sees it. `patterns`
/// is sorted; the implicit subscription is `["#"]`. `scope` `None` means
/// all workspaces.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Subscription {
    /// Sorted patterns.
    pub patterns: Vec<String>,
    /// Workspace scope, `None` for all.
    pub scope: Option<Scope>,
}

/// A link's subscription set and scope. `implicit` is the initial `#`
/// that the first explicit subscription replaces.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SubState {
    /// The implicit `#` is in force; `patterns` is then empty.
    pub implicit: bool,
    /// The explicit pattern set.
    pub patterns: BTreeSet<String>,
    /// Workspace scope, `None` for all.
    pub scope: Option<Scope>,
}

impl Default for SubState {
    fn default() -> Self {
        Self::new()
    }
}

impl SubState {
    /// The initial state: the implicit `#`, no scope.
    pub fn new() -> Self {
        SubState {
            implicit: true,
            patterns: BTreeSet::new(),
            scope: None,
        }
    }

    /// Returns the state with an explicit pattern set.
    pub fn with_patterns<I: IntoIterator<Item = String>>(mut self, ps: I) -> Self {
        self.implicit = false;
        self.patterns = ps.into_iter().collect();
        self
    }

    /// The explicit patterns, sorted bytewise.
    pub fn sorted_patterns(&self) -> Vec<String> {
        self.patterns.iter().cloned().collect()
    }

    /// What an authorizer sees.
    pub fn effective(&self) -> Subscription {
        let patterns = if self.implicit {
            vec!["#".to_string()]
        } else {
            self.sorted_patterns()
        };
        Subscription {
            patterns,
            scope: self.scope.clone(),
        }
    }

    /// Returns the state after `r`, or a limit error; all or nothing.
    pub fn apply(&self, r: &CtlRequest, max: usize) -> Result<SubState, StreamError> {
        let mut next = self.clone();
        match r.typ {
            RequestType::Subscribe => {
                if let Some(ps) = &r.patterns {
                    if next.implicit {
                        next.implicit = false;
                        next.patterns.clear();
                    }
                    next.patterns.extend(ps.iter().cloned());
                    if next.patterns.len() > max {
                        return Err(StreamError::new(
                            CODE_LIMIT,
                            format!("more than {max} subscriptions"),
                        ));
                    }
                }
                if let Some(s) = &r.scope {
                    next.scope = Some(s.clone());
                }
            }
            RequestType::Unsubscribe => {
                if next.implicit {
                    next.implicit = false;
                    next.patterns = BTreeSet::from(["#".to_string()]);
                }
                for p in r.patterns.iter().flatten() {
                    next.patterns.remove(p);
                }
            }
        }
        Ok(next)
    }

    /// Reports whether `topic` matches the set.
    pub fn matches(&self, topic: &crate::bus::Topic) -> bool {
        self.implicit || self.patterns.iter().any(|p| topic.matches(p))
    }
}

/// The per-link delivery predicate, before the adapter's link filter
/// callback. Gap markers always pass; an entry from the peer's own origin
/// never does (an empty peer origin excludes nothing).
pub fn entry_delivers(s: &SubState, peer_origin: &str, e: &LogEntry) -> bool {
    if e.marker {
        return true;
    }
    if !peer_origin.is_empty() && e.origin == peer_origin {
        return false;
    }
    s.matches(&e.event.topic) && Scope::allows(s.scope.as_ref(), &e.event.workspace_id)
}
