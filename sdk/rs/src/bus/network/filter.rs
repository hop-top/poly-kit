//! [`TopicFilter`]: Go's `network_filter.go`.

use crate::bus::topic::Topic;

/// Which topics the adapter forwards to peers. Deny patterns are checked
/// first: any match blocks the topic. The allow list is checked next:
/// `None` (Go's nil) passes everything, an empty list passes nothing.
/// Patterns are subscribe patterns (`*`, `#`).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TopicFilter {
    /// Patterns to forward; `None` forwards every topic deny lets
    /// through.
    pub allow: Option<Vec<String>>,
    /// Patterns never forwarded; checked first.
    pub deny: Vec<String>,
}

impl TopicFilter {
    /// A filter forwarding only topics matching `patterns`.
    pub fn allow<I, S>(patterns: I) -> Self
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        TopicFilter {
            allow: Some(patterns.into_iter().map(Into::into).collect()),
            deny: Vec::new(),
        }
    }

    /// A filter forwarding every topic but those matching `patterns`.
    pub fn deny<I, S>(patterns: I) -> Self
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        TopicFilter::default().with_deny(patterns)
    }

    /// Replaces the deny list.
    pub fn with_deny<I, S>(mut self, patterns: I) -> Self
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        self.deny = patterns.into_iter().map(Into::into).collect();
        self
    }

    /// Whether `topic` should be forwarded: Go's `Match`.
    pub fn matches(&self, topic: &str) -> bool {
        let t = Topic::from(topic);
        if self.deny.iter().any(|p| t.matches(p)) {
            return false;
        }
        match &self.allow {
            None => true,
            Some(allow) => allow.iter().any(|p| t.matches(p)),
        }
    }
}
