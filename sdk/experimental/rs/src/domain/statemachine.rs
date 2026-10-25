//! The state machine: rules, the transition check, its error, and the
//! pre/post-transition events.
//!
//! # Mirrored from Go
//!
//! - Without `force`, a `from` with no rules fails with
//!   [`TransitionError::allowed`] `None`, and a `to` outside the rules
//!   with a copy of them. `force` skips the check.
//! - With a publisher, the pre-transition event is published on the
//!   caller's thread; an error vetoes the transition, wrapped as
//!   `pre-transition veto: <err>`. A rejected or vetoed transition
//!   publishes no post-transition event.
//! - The post-transition event is fire-and-forget: its error is dropped
//!   and the caller does not wait for it. Both events carry the caller's
//!   context, [`SOURCE`] and a [`TransitionPayload`] whose JSON is Go's,
//!   byte for byte ([`TransitionPayload::to_json`]).
//!
//! # Where this differs from Go
//!
//! - Go starts a goroutine for the post-transition publish. Here it runs
//!   on the [`Spawn`] given to [`StateMachine::with_spawner`]; without
//!   one, on a thread of its own, except on wasm32-unknown-unknown,
//!   which has no threads: there it runs inline, after the
//!   pre-transition publish and before `transition` returns. Give that
//!   target a spawner (e.g. one calling
//!   `wasm_bindgen_futures::spawn_local`) to keep it off the caller.
//! - Go's `errors.Is(err, ErrInvalidTransition)` is
//!   [`StateMachineError::is_invalid_transition`], or
//!   [`is_invalid_transition`] for any error chain; `errors.Is(err,
//!   vetoErr)` is a downcast of [`Error::source`].
//! - Go's publisher takes `payload any`; here it takes a [`Payload`],
//!   matched where Go type-asserts.

use std::collections::HashMap;
use std::error::Error;
use std::fmt;
use std::sync::Arc;

use serde::{Deserialize, Serialize};

#[cfg(doc)]
use crate::bus::Task;
use crate::bus::{Context, Spawn};

use super::quote::quote;
use super::topics::StateMachineTopics;

/// The `source` every state machine event is published with.
pub const SOURCE: &str = "domain.statemachine";

/// A state label, e.g. `"open"` or `"closed"`. Go's `domain.State`.
#[derive(Clone, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
#[serde(transparent)]
pub struct State(pub String);

impl State {
    /// The label.
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl From<&str> for State {
    fn from(s: &str) -> Self {
        State(s.to_string())
    }
}

impl From<String> for State {
    fn from(s: String) -> Self {
        State(s)
    }
}

impl fmt::Display for State {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

/// Transition rules: each state maps to the states it may move to.
pub type Rules = HashMap<State, Vec<State>>;

/// A transition the rules do not allow. Go's `*TransitionError`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TransitionError {
    /// The state the transition started from.
    pub from: State,
    /// The state it tried to reach.
    pub to: State,
    /// The states `from` may move to; `None` when `from` has no rules,
    /// Go's nil, as against `Some(vec![])`, rules that allow nothing.
    pub allowed: Option<Vec<State>>,
}

impl TransitionError {
    /// Always true: Go's `errors.Is(err, ErrInvalidTransition)`.
    pub fn is_invalid_transition(&self) -> bool {
        true
    }
}

impl fmt::Display for TransitionError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let (from, to) = (quote(self.from.as_str()), quote(self.to.as_str()));
        match &self.allowed {
            None => write!(
                f,
                "invalid state transition: no rules for state {from} (attempted {from} → {to})"
            ),
            Some(allowed) => {
                let allowed: Vec<&str> = allowed.iter().map(State::as_str).collect();
                write!(
                    f,
                    "invalid state transition: {from} → {to} (allowed: [{}])",
                    allowed.join(" ")
                )
            }
        }
    }
}

impl Error for TransitionError {}

/// The error a publisher returns to veto a pre-transition event.
pub type PublishError = Box<dyn Error + Send + Sync + 'static>;

/// Why [`StateMachine::transition`] failed.
#[derive(Debug)]
pub enum StateMachineError {
    /// The rules do not allow the transition.
    Invalid(TransitionError),
    /// The pre-transition publish failed.
    Veto(PublishError),
}

impl StateMachineError {
    /// Go's `errors.Is(err, ErrInvalidTransition)`: true for
    /// [`Invalid`](Self::Invalid), and for a veto whose cause chain holds
    /// a [`TransitionError`], as Go's `%w` wrap is unwrapped.
    pub fn is_invalid_transition(&self) -> bool {
        is_invalid_transition(self)
    }
}

impl fmt::Display for StateMachineError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            StateMachineError::Invalid(e) => e.fmt(f),
            StateMachineError::Veto(e) => write!(f, "pre-transition veto: {e}"),
        }
    }
}

impl Error for StateMachineError {
    /// The publisher's error for a veto; `None` for an invalid
    /// transition, whose text is already the whole message.
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            StateMachineError::Invalid(_) => None,
            StateMachineError::Veto(e) => Some(e.as_ref()),
        }
    }
}

/// Go's `errors.Is(err, ErrInvalidTransition)` over any error chain:
/// true when `err` or any [`Error::source`] below it is a
/// [`TransitionError`] or a [`StateMachineError::Invalid`].
pub fn is_invalid_transition(err: &(dyn Error + 'static)) -> bool {
    let mut cur = Some(err);
    while let Some(e) = cur {
        if e.is::<TransitionError>()
            || matches!(
                e.downcast_ref::<StateMachineError>(),
                Some(StateMachineError::Invalid(_))
            )
        {
            return true;
        }
        cur = e.source();
    }
    false
}

/// The payload of both transition events. Go's `TransitionPayload`,
/// which has no json tags, so its keys are the Go field names: `From`,
/// `To`, `Force`, in that order.
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "PascalCase")]
pub struct TransitionPayload {
    /// The state the transition started from.
    pub from: State,
    /// The state it reached.
    pub to: State,
    /// Whether the rules check was skipped.
    pub force: bool,
}

impl TransitionPayload {
    /// The bytes Go's `json.Marshal` writes for this payload.
    ///
    /// `serde_json` agrees with Go except that Go also escapes `<`, `>`,
    /// `&`, U+2028 and U+2029; this applies those escapes. Serializing
    /// with `serde_json` directly gives the same keys and order without
    /// them.
    pub fn to_json(&self) -> String {
        go_json(self)
    }
}

/// `value` as Go's `encoding/json` writes it: `serde_json`'s compact
/// output with Go's HTML-safe and line-separator escapes. Those
/// characters can only occur inside JSON strings, so replacing them in
/// the whole document never touches the structure.
fn go_json(value: &impl Serialize) -> String {
    // Serializing derived structs of strings and bools cannot fail.
    let raw = serde_json::to_string(value).unwrap_or_default();
    let mut out = String::with_capacity(raw.len());
    for c in raw.chars() {
        match c {
            '<' => out.push_str("\\u003c"),
            '>' => out.push_str("\\u003e"),
            '&' => out.push_str("\\u0026"),
            '\u{2028}' => out.push_str("\\u2028"),
            '\u{2029}' => out.push_str("\\u2029"),
            c => out.push(c),
        }
    }
    out
}

/// What an [`EventPublisher`] is handed: Go's `payload any`, as an
/// enum to match where Go type-asserts. Serializes as the inner payload.
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(untagged)]
#[non_exhaustive]
pub enum Payload {
    /// A state machine transition.
    Transition(TransitionPayload),
}

impl Payload {
    /// The bytes Go's `json.Marshal` writes for this payload.
    pub fn to_json(&self) -> String {
        match self {
            Payload::Transition(p) => p.to_json(),
        }
    }
}

/// Publishes domain events. Go's `domain.EventPublisher`; any
/// `Fn(&Context, &str, &str, &Payload) -> Result<(), PublishError>`
/// closure is one.
///
/// `topic` and `source` are dot-separated strings. The state machine
/// calls it on the caller's thread for the pre-transition event and,
/// for the post-transition event, from wherever that runs (see the
/// module docs), so it must be `Send + Sync`.
pub trait EventPublisher: Send + Sync {
    /// Publishes one event. An error vetoes a pre-transition event and is
    /// dropped for a post-transition one.
    fn publish(
        &self,
        ctx: &Context,
        topic: &str,
        source: &str,
        payload: &Payload,
    ) -> Result<(), PublishError>;
}

impl<F> EventPublisher for F
where
    F: Fn(&Context, &str, &str, &Payload) -> Result<(), PublishError> + Send + Sync,
{
    fn publish(
        &self,
        ctx: &Context,
        topic: &str,
        source: &str,
        payload: &Payload,
    ) -> Result<(), PublishError> {
        self(ctx, topic, source, payload)
    }
}

/// Enforces allowed state transitions and, given an [`EventPublisher`],
/// publishes the pre- and post-transition events. Go's
/// `domain.StateMachine`.
///
/// Go's functional options are builder methods: [`with_topic_prefix`],
/// [`with_topics`], plus [`with_spawner`] for the post-transition event.
/// Applied in order, the last topic option wins, as in Go.
///
/// [`with_topic_prefix`]: StateMachine::with_topic_prefix
/// [`with_topics`]: StateMachine::with_topics
/// [`with_spawner`]: StateMachine::with_spawner
pub struct StateMachine {
    pub(super) rules: Rules,
    pub(super) publisher: Option<Arc<dyn EventPublisher>>,
    pub(super) topics: StateMachineTopics,
    pub(super) spawner: Option<Arc<dyn Spawn>>,
}

impl StateMachine {
    /// A machine over `rules`, publishing through `publisher` when one
    /// is given (`None`, Go's nil, disables the events), on
    /// [`StateMachineTopics::default`]. Go's `NewStateMachine`.
    pub fn new(rules: Rules, publisher: Option<Arc<dyn EventPublisher>>) -> Self {
        StateMachine {
            rules,
            publisher,
            topics: StateMachineTopics::default(),
            spawner: None,
        }
    }

    /// Runs each post-transition publish as a [`Task`] on `spawner`,
    /// the executor seam the bus's async subscriptions use, in place of
    /// the default thread (inline on wasm32-unknown-unknown). The task
    /// may be dropped unrun: the event is best-effort.
    pub fn with_spawner(mut self, spawner: Arc<dyn Spawn>) -> Self {
        self.spawner = Some(spawner);
        self
    }

    /// The states `state` may move to, as a copy; `None` when it has no
    /// rules. Go's `AllowedFrom`.
    pub fn allowed_from(&self, state: impl Into<State>) -> Option<Vec<State>> {
        self.rules.get(&state.into()).cloned()
    }

    /// Validates and executes the change `from` → `to`. Go's
    /// `Transition`; see the module docs for the events.
    ///
    /// # Errors
    ///
    /// [`StateMachineError::Invalid`] when the rules do not allow it and
    /// `force` is false; [`StateMachineError::Veto`] when the
    /// pre-transition publish fails.
    pub fn transition(
        &self,
        ctx: &Context,
        from: impl Into<State>,
        to: impl Into<State>,
        force: bool,
    ) -> Result<(), StateMachineError> {
        let (from, to) = (from.into(), to.into());
        if !force {
            match self.rules.get(&from) {
                None => {
                    return Err(StateMachineError::Invalid(TransitionError {
                        from,
                        to,
                        allowed: None,
                    }))
                }
                Some(allowed) if !allowed.contains(&to) => {
                    return Err(StateMachineError::Invalid(TransitionError {
                        from,
                        to,
                        allowed: Some(allowed.clone()),
                    }))
                }
                Some(_) => {}
            }
        }

        let Some(publisher) = &self.publisher else {
            return Ok(());
        };
        let payload = Payload::Transition(TransitionPayload { from, to, force });

        // Pre-transition: on the caller's thread, veto-able.
        publisher
            .publish(ctx, self.topics.pre_transitioned.as_str(), SOURCE, &payload)
            .map_err(StateMachineError::Veto)?;

        // Post-transition: fire-and-forget.
        let (publisher, ctx) = (Arc::clone(publisher), ctx.clone());
        let topic = self.topics.post_transitioned.clone();
        let post = move || {
            let _ = publisher.publish(&ctx, topic.as_str(), SOURCE, &payload);
        };
        match &self.spawner {
            Some(spawner) => spawner.spawn(Box::pin(async move { post() })),
            None => spawn_default(post),
        }
        Ok(())
    }
}

/// Go's `go func() { ... }()`: a thread of its own. If the thread cannot
/// be started the event is dropped, as a best-effort event may be.
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
fn spawn_default(post: impl FnOnce() + Send + 'static) {
    let _ = std::thread::Builder::new()
        .name("domain-statemachine-post".into())
        .spawn(post);
}

/// No threads on this target: run inline.
#[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
fn spawn_default(post: impl FnOnce() + Send + 'static) {
    post();
}
