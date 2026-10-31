//! Typed topic construction: [`topic_of`], [`prefixed_topic_of`] and
//! [`parse_topic`]. Mirrors Go `go/runtime/bus/builder.go`.
//!
//! A [`TopicBuilder`] carries an optional snake_case modifier that joins
//! into the Object segment with an underscore on render. The wire form
//! of `topic_of("kit", "config", "snapshot").modifier("reload").action("failed")`
//! is `kit.config.snapshot_reload.failed` — the modifier is NOT a fifth
//! dot segment, so the 4-segment grammar holds.

use super::topic::{validate_topic, InvalidTopicError, Topic};

/// Value-typed builder for [`Topic`] strings following
/// `[Source].[Category].[Object].[Action]`.
///
/// Setters consume and return the builder; it is [`Clone`], and
/// [`action`](Self::action) borrows, so one builder can render several
/// actions. Nothing is validated until [`action`](Self::action) or
/// [`try_action`](Self::try_action).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TopicBuilder {
    source: String,
    category: String,
    object: String,
    modifier: String,
}

/// Starts a [`TopicBuilder`] for the given Source, Category and Object.
///
/// ```
/// # use hop_top_kit::bus::topic_of;
/// let t = topic_of("kit", "config", "snapshot").modifier("reload").action("failed");
/// assert_eq!(t.as_str(), "kit.config.snapshot_reload.failed");
/// ```
pub fn topic_of(
    source: impl Into<String>,
    category: impl Into<String>,
    object: impl Into<String>,
) -> TopicBuilder {
    TopicBuilder {
        source: source.into(),
        category: category.into(),
        object: object.into(),
        modifier: String::new(),
    }
}

/// [`topic_of`] with the modifier supplied inline. Mirrors Go's variadic
/// argument: at most one modifier is honoured, the last wins, and an
/// empty slice means none.
pub fn prefixed_topic_of(
    source: impl Into<String>,
    category: impl Into<String>,
    object: impl Into<String>,
    modifier: &[&str],
) -> TopicBuilder {
    let mut b = topic_of(source, category, object);
    if let Some(m) = modifier.last() {
        b.modifier = (*m).to_string();
    }
    b
}

impl TopicBuilder {
    /// Sets the modifier. Last wins; an empty string clears it. The
    /// modifier is validated as part of the Object segment on render.
    pub fn modifier(mut self, m: impl Into<String>) -> Self {
        self.modifier = m.into();
        self
    }

    /// Replaces the Source segment.
    pub fn source(mut self, s: impl Into<String>) -> Self {
        self.source = s.into();
        self
    }

    /// Replaces the Category segment.
    pub fn category(mut self, c: impl Into<String>) -> Self {
        self.category = c.into();
        self
    }

    /// Replaces the Object segment. The modifier is preserved; clear it
    /// with `modifier("")` if the new object should stand alone.
    pub fn object(mut self, o: impl Into<String>) -> Self {
        self.object = o.into();
        self
    }

    /// The raw Source segment.
    pub fn source_seg(&self) -> &str {
        &self.source
    }

    /// The raw Category segment.
    pub fn category_seg(&self) -> &str {
        &self.category
    }

    /// The raw Object segment, without the modifier.
    pub fn object_seg(&self) -> &str {
        &self.object
    }

    /// The raw modifier, empty when none.
    pub fn modifier_seg(&self) -> &str {
        &self.modifier
    }

    /// Renders the builder into a topic validated by [`validate_topic`]
    /// (4 segments, `[a-z0-9_]`, past-tense action).
    ///
    /// # Panics
    ///
    /// On any validation failure, with Go's text
    /// `bus.TopicOf: bus: invalid topic: "<topic>" (<reason>)`. A panic
    /// here is a caller bug; use [`try_action`](Self::try_action) for
    /// input that is not a literal.
    pub fn action(&self, action: &str) -> Topic {
        match self.try_action(action) {
            Ok(t) => t,
            Err(e) => panic!("bus.TopicOf: {e}"),
        }
    }

    /// Non-panicking [`action`](Self::action).
    pub fn try_action(&self, action: &str) -> Result<Topic, InvalidTopicError> {
        let object = if self.modifier.is_empty() {
            self.object.clone()
        } else {
            format!("{}_{}", self.object, self.modifier)
        };
        let t = Topic(format!(
            "{}.{}.{}.{}",
            self.source, self.category, object, action
        ));
        match validate_topic(&t) {
            Ok(()) => Ok(t),
            Err(reason) => Err(InvalidTopicError { topic: t, reason }),
        }
    }
}

/// Inverse of [`TopicBuilder::action`]: validates `s` with
/// [`validate_topic`] and returns the populated builder plus the action.
///
/// The Object segment splits on its FIRST underscore into object and
/// modifier: `snapshot_partial_reload` gives object `snapshot`, modifier
/// `partial_reload`. On failure the error carries the input topic and
/// [`validate_topic`]'s message as its reason.
pub fn parse_topic(s: &str) -> Result<(TopicBuilder, String), InvalidTopicError> {
    let t = Topic::from(s);
    if let Err(reason) = validate_topic(&t) {
        return Err(InvalidTopicError { topic: t, reason });
    }
    // validate_topic guarantees exactly 4 segments.
    let mut parts = s.splitn(4, '.');
    let (source, category, object_seg, action) = (
        parts.next().unwrap_or_default(),
        parts.next().unwrap_or_default(),
        parts.next().unwrap_or_default(),
        parts.next().unwrap_or_default(),
    );
    let (object, modifier) = object_seg.split_once('_').unwrap_or((object_seg, ""));
    Ok((
        TopicBuilder {
            source: source.to_string(),
            category: category.to_string(),
            object: object.to_string(),
            modifier: modifier.to_string(),
        },
        action.to_string(),
    ))
}
