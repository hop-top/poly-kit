//! In-memory pub/sub dispatch.
//!
//! # Dispatch model: synchronous, thread-safe
//!
//! The Go canonical runtime (`go/runtime/bus/bus.go`) splits subscribers
//! into two kinds. Sync handlers run inline and may veto the publish;
//! async handlers run on goroutines drawn from a bounded semaphore pool,
//! with a `WaitGroup` drained by `Close` under a caller deadline.
//!
//! This module ships **synchronous dispatch only**, for now. The earlier
//! decision to leave async delivery out for good is superseded: a
//! networked, multi-threaded Rust consumer needs it. The plan, in
//! `docs/contributors/architecture/rust-bus-parity.md`, is:
//!
//! - an async dispatch path with a bounded pool and a draining close,
//!   with tokio behind its own feature so the `bus` feature and the
//!   `wasm32-unknown-unknown` build stay free of an async runtime;
//! - the network adapter and the adapter seam it plugs into behind
//!   their own features.
//!
//! Until those land, synchronous delivery is a strict subset of the
//! async contract from the subscriber's point of view: a handler that
//! would have run on a pool thread instead runs on the publisher's
//! thread. Handlers that want to defer work can spawn a task themselves.
//!
//! Consequence to be aware of: a slow handler blocks the publisher, and
//! [`Bus::publish`] returns only once every matching subscriber has run.
//!
//! # Threads, context and re-entrancy
//!
//! [`Bus`] and [`SharedBus`] are `Send + Sync`, and so must handlers and
//! reporters be. Publishing from several threads at once is allowed, as
//! in Go. A plain [`Bus`] needs `&mut` to subscribe, unsubscribe or
//! close, so nothing can be in flight then; [`SharedBus`] takes `&self`
//! everywhere, and its unsubscribe and close drain in-flight dispatch.
//! The threading, draining and re-entrancy rules live on [`SharedBus`].
//!
//! Handlers receive the publisher's [`Context`], as Go's receive its
//! `ctx`. [`Bus::publish`] is [`Bus::publish_ctx`] with
//! [`Context::background`], and [`Bus::subscribe`] registers a handler
//! that ignores the context. As in Go, publish never reads the context
//! itself: a canceled or expired one reaches the handlers, which decide
//! what it means.
//!
//! Everything else is behaviour-preserving against Go: subscription
//! identity and removal, first-error veto semantics, publish-after-close
//! rejection, and the [`Mode`] enforcement ladder.

use std::fmt;
use std::sync::Arc;

use super::context::{Context, ContextError};
use super::event::Event;
pub use super::shared::SharedBus;
use super::topic::{validate, InvalidTopicError, Topic};

/// A synchronous event handler, given the publisher's [`Context`].
/// Returning an error vetoes the publish — no further handler runs for
/// that event, and the error surfaces to the publisher. Go's `Handler`.
pub type Handler = Box<dyn Fn(&Context, &Event) -> Result<(), BusError> + Send + Sync>;

/// Receives errors from non-critical operations (e.g. topic-naming
/// violations in [`Mode::Warn`]).
pub type ErrFn = Box<dyn Fn(&BusError) + Send + Sync>;

/// Reference-counted forms of [`Handler`] and [`ErrFn`], so a dispatch
/// can run them after the bus lock is released.
pub(super) type SharedHandler = Arc<dyn Fn(&Context, &Event) -> Result<(), BusError> + Send + Sync>;
pub(super) type SharedErrFn = Arc<dyn Fn(&BusError) + Send + Sync>;

/// Controls how the bus enforces topic naming on publish.
///
/// The naming contract is the 4-segment form
/// `[Source].[Category].[Object].[Action]` — see
/// [`validate`] for the exact rules.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Mode {
    /// Disables topic validation. Publish never rejects on naming and
    /// never reports.
    Off,
    /// Validates topics and reports violations via the configured
    /// reporter, but still delivers the event. The default.
    #[default]
    Warn,
    /// Validates topics and rejects publishes that violate the naming
    /// contract; the event is not delivered.
    Strict,
}

impl fmt::Display for Mode {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            Mode::Off => "off",
            Mode::Warn => "warn",
            Mode::Strict => "strict",
        })
    }
}

/// Errors surfaced by the bus.
#[derive(Debug)]
pub enum BusError {
    /// A published topic violated the naming contract.
    InvalidTopic(InvalidTopicError),
    /// Publish was called after [`Bus::close`].
    Closed,
    /// A subscriber vetoed the publish.
    Handler(String),
    /// Returned only by [`SharedBus::close_ctx`], when its context is
    /// done before the async drain finishes. Displays Go's `ctx.Err()`
    /// text. Publish never returns it.
    Context(ContextError),
}

impl BusError {
    /// Builds a handler-veto error from any message.
    pub fn handler(msg: impl Into<String>) -> Self {
        BusError::Handler(msg.into())
    }

    /// Reports whether this is a topic-naming violation. The analogue of
    /// Go's `errors.Is(err, ErrInvalidTopic)`.
    pub fn is_invalid_topic(&self) -> bool {
        matches!(self, BusError::InvalidTopic(_))
    }
}

impl fmt::Display for BusError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            BusError::InvalidTopic(e) => write!(f, "{e}"),
            BusError::Closed => f.write_str("bus: publish after close"),
            BusError::Handler(m) => f.write_str(m),
            BusError::Context(e) => write!(f, "{e}"),
        }
    }
}

impl std::error::Error for BusError {}

impl From<ContextError> for BusError {
    fn from(e: ContextError) -> Self {
        BusError::Context(e)
    }
}

impl From<InvalidTopicError> for BusError {
    fn from(e: InvalidTopicError) -> Self {
        BusError::InvalidTopic(e)
    }
}

/// Handle returned by [`Bus::subscribe`]; dropping it does nothing, but
/// passing it to [`Bus::unsubscribe`] removes the subscription.
///
/// Go returns an `Unsubscribe` closure that captures the bus. An owning
/// closure would force the bus behind a shared cell purely to support
/// removal, so this port returns an opaque id instead — same capability,
/// no borrow gymnastics for callers who never unsubscribe.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub struct SubscriptionId(pub(super) u64);

pub(super) struct Subscription {
    pub(super) id: SubscriptionId,
    pub(super) pattern: String,
    pub(super) handler: SharedHandler,
}

/// In-memory publish/subscribe bus.
///
/// See the [module docs](self) for the dispatch model.
pub struct Bus {
    pub(super) subs: Vec<Subscription>,
    pub(super) next_id: u64,
    pub(super) closed: bool,
    pub(super) enforce: Mode,
    pub(super) on_invalid: Option<SharedErrFn>,
}

impl Default for Bus {
    fn default() -> Self {
        Self::new()
    }
}

impl Bus {
    /// Creates a bus with the default enforcement mode ([`Mode::Warn`])
    /// and no violation reporter.
    pub fn new() -> Self {
        Bus {
            subs: Vec::new(),
            next_id: 0,
            closed: false,
            enforce: Mode::Warn,
            on_invalid: None,
        }
    }

    /// Starts a [`BusBuilder`].
    pub fn builder() -> BusBuilder {
        BusBuilder::default()
    }

    /// Registers `handler` for every event whose topic matches `pattern`.
    ///
    /// `pattern` supports the wildcards described on [`Topic::matches`].
    /// The handler does not see the publish context; use
    /// [`Bus::subscribe_ctx`] for one that does.
    pub fn subscribe(
        &mut self,
        pattern: impl Into<String>,
        handler: impl Fn(&Event) -> Result<(), BusError> + Send + Sync + 'static,
    ) -> SubscriptionId {
        self.subscribe_ctx(pattern, move |_, e| handler(e))
    }

    /// Registers `handler`, which receives the publisher's [`Context`],
    /// for every event whose topic matches `pattern`. Go's `Subscribe`.
    pub fn subscribe_ctx(
        &mut self,
        pattern: impl Into<String>,
        handler: impl Fn(&Context, &Event) -> Result<(), BusError> + Send + Sync + 'static,
    ) -> SubscriptionId {
        let id = SubscriptionId(self.next_id);
        self.next_id += 1;
        self.subs.push(Subscription {
            id,
            pattern: pattern.into(),
            handler: Arc::new(handler),
        });
        id
    }

    /// Removes a subscription. Returns whether it was found.
    pub fn unsubscribe(&mut self, id: SubscriptionId) -> bool {
        let Some(i) = self.subs.iter().position(|s| s.id == id) else {
            return false;
        };
        self.subs.remove(i);
        true
    }

    /// [`Bus::publish_ctx`] with [`Context::background`].
    pub fn publish(&self, event: &Event) -> Result<(), BusError> {
        self.publish_ctx(&Context::background(), event)
    }

    /// Delivers `event` to every matching subscriber, in subscription
    /// order, passing `ctx` to each. The first handler error vetoes the
    /// publish: no later handler runs, and the error is returned. Go's
    /// `Publish`.
    ///
    /// Checks run in Go's order: topic, then closed. `ctx` is not
    /// checked, before or between handlers: a canceled or expired
    /// context is handed to the handlers like any other, as Go's
    /// in-memory bus does.
    pub fn publish_ctx(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        check_topic(self.enforce, self.on_invalid.as_ref(), &event.topic)?;
        if self.closed {
            return Err(BusError::Closed);
        }
        for s in &self.subs {
            if event.topic.matches(&s.pattern) {
                (s.handler)(ctx, event)?;
            }
        }
        Ok(())
    }

    /// Stops the bus from accepting new publishes and drops every
    /// subscription.
    ///
    /// Go's `Close` drains in-flight dispatch under a caller deadline.
    /// On a plain `Bus` there is nothing in flight — `close` takes
    /// `&mut self`, so every `publish` has already returned — and this
    /// takes no context and cannot time out. [`SharedBus::close_ctx`] is
    /// the draining form.
    pub fn close(&mut self) {
        self.closed = true;
        self.subs.clear();
    }

    /// Reports whether [`Bus::close`] has been called.
    pub fn is_closed(&self) -> bool {
        self.closed
    }

    /// The configured enforcement mode.
    pub fn enforce_mode(&self) -> Mode {
        self.enforce
    }
}

/// Applies the enforcement mode `enforce` to `t`. The single point where
/// publish enforces topic naming.
///
/// - [`Mode::Off`]: returns `Ok`; the reporter is not invoked.
/// - [`Mode::Warn`]: invokes the reporter on failure, returns `Ok` so
///   the publish proceeds.
/// - [`Mode::Strict`]: invokes the reporter on failure and returns the
///   error so the publish is aborted.
pub(super) fn check_topic(
    enforce: Mode,
    on_invalid: Option<&SharedErrFn>,
    t: &Topic,
) -> Result<(), BusError> {
    if enforce == Mode::Off {
        return Ok(());
    }
    let Err(e) = validate(t) else {
        return Ok(());
    };
    let err = BusError::InvalidTopic(e);
    if let Some(reporter) = on_invalid {
        reporter(&err);
    }
    if enforce == Mode::Strict {
        return Err(err);
    }
    Ok(())
}

/// Builds a [`Bus`] with non-default options. The analogue of Go's
/// variadic `Option` constructor.
#[derive(Default)]
pub struct BusBuilder {
    enforce: Option<Mode>,
    on_invalid: Option<SharedErrFn>,
}

impl BusBuilder {
    /// Sets the topic-naming enforcement mode. Defaults to
    /// [`Mode::Warn`].
    pub fn enforce(mut self, m: Mode) -> Self {
        self.enforce = Some(m);
        self
    }

    /// Installs a callback invoked when publish encounters a topic that
    /// fails validation. In [`Mode::Warn`] the reporter fires and the
    /// event is still delivered; in [`Mode::Strict`] it fires and publish
    /// also returns the error.
    pub fn invalid_topic_reporter(mut self, f: impl Fn(&BusError) + Send + Sync + 'static) -> Self {
        self.on_invalid = Some(Arc::new(f));
        self
    }

    /// Finalises the bus.
    pub fn build(self) -> Bus {
        Bus {
            subs: Vec::new(),
            next_id: 0,
            closed: false,
            enforce: self.enforce.unwrap_or_default(),
            on_invalid: self.on_invalid,
        }
    }
}
