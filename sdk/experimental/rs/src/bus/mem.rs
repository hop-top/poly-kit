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
//! # Threads and re-entrancy
//!
//! [`Bus`] and [`SharedBus`] are `Send + Sync`, and so must handlers and
//! reporters be. Publishing from several threads at once is allowed, as
//! in Go.
//!
//! A handler may publish on the bus that is dispatching to it. As in Go,
//! the inner publish delivers to every matching subscriber and returns
//! before the outer dispatch moves on, and its veto reaches the handler
//! as an ordinary error. [`SharedBus`] snapshots the matching handlers
//! under its lock and runs them with the lock released, so a handler may
//! also subscribe or unsubscribe re-entrantly. Go deadlocks there,
//! because its sync handlers run under the read lock. One consequence of
//! the snapshot: a subscription removed mid-dispatch still receives the
//! event already in flight.
//!
//! Everything else is behaviour-preserving against Go: subscription
//! identity and removal, first-error veto semantics, publish-after-close
//! rejection, and the [`Mode`] enforcement ladder.

use std::fmt;
use std::sync::{Arc, PoisonError, RwLock, RwLockReadGuard, RwLockWriteGuard};

use super::event::Event;
use super::topic::{validate, InvalidTopicError, Topic};

/// A synchronous event handler. Returning an error vetoes the publish —
/// no further handler runs for that event, and the error surfaces to the
/// publisher.
pub type Handler = Box<dyn Fn(&Event) -> Result<(), BusError> + Send + Sync>;

/// Receives errors from non-critical operations (e.g. topic-naming
/// violations in [`Mode::Warn`]).
pub type ErrFn = Box<dyn Fn(&BusError) + Send + Sync>;

/// Reference-counted forms of [`Handler`] and [`ErrFn`], so a dispatch
/// can run them after the bus lock is released.
type SharedHandler = Arc<dyn Fn(&Event) -> Result<(), BusError> + Send + Sync>;
type SharedErrFn = Arc<dyn Fn(&BusError) + Send + Sync>;

/// Controls how the bus enforces topic naming on publish.
///
/// The naming contract is the 4-segment form
/// `[Source].[Category].[Object].[Action]` — see
/// [`validate`](super::validate) for the exact rules.
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
        }
    }
}

impl std::error::Error for BusError {}

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
pub struct SubscriptionId(u64);

struct Subscription {
    id: SubscriptionId,
    pattern: String,
    handler: SharedHandler,
}

/// In-memory publish/subscribe bus.
///
/// See the [module docs](self) for the dispatch model.
pub struct Bus {
    subs: Vec<Subscription>,
    next_id: u64,
    closed: bool,
    enforce: Mode,
    on_invalid: Option<SharedErrFn>,
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
    pub fn subscribe(
        &mut self,
        pattern: impl Into<String>,
        handler: impl Fn(&Event) -> Result<(), BusError> + Send + Sync + 'static,
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
        let before = self.subs.len();
        self.subs.retain(|s| s.id != id);
        self.subs.len() != before
    }

    /// Delivers `event` to every matching subscriber, in subscription
    /// order. The first handler error vetoes the publish: no later
    /// handler runs, and the error is returned.
    pub fn publish(&self, event: &Event) -> Result<(), BusError> {
        self.dispatch(event).run(event)
    }

    /// Stops the bus from accepting new publishes and drops every
    /// subscription.
    ///
    /// Go's `Close` also drains in-flight async handlers under a caller
    /// deadline. With synchronous dispatch there is nothing in flight —
    /// `publish` has already returned by the time `close` can be called —
    /// so this takes no context and cannot time out.
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

    /// Captures what a publish of `event` needs, so a caller holding a
    /// lock around the bus can release it before any handler runs.
    fn dispatch(&self, event: &Event) -> Dispatch {
        let handlers = if self.closed {
            Vec::new()
        } else {
            self.subs
                .iter()
                .filter(|s| event.topic.matches(&s.pattern))
                .map(|s| Arc::clone(&s.handler))
                .collect()
        };
        Dispatch {
            enforce: self.enforce,
            on_invalid: self.on_invalid.clone(),
            closed: self.closed,
            handlers,
        }
    }
}

/// One publish captured from a [`Bus`]: its enforcement settings and
/// the matching handlers, in subscription order.
struct Dispatch {
    enforce: Mode,
    on_invalid: Option<SharedErrFn>,
    closed: bool,
    handlers: Vec<SharedHandler>,
}

impl Dispatch {
    /// Checks the topic, then the closed flag, then runs the handlers
    /// until the first veto. Same order as Go's `Publish`.
    fn run(self, event: &Event) -> Result<(), BusError> {
        self.check_topic(&event.topic)?;
        if self.closed {
            return Err(BusError::Closed);
        }
        for h in &self.handlers {
            h(event)?;
        }
        Ok(())
    }

    /// Applies the configured enforcement mode to `t`. The single point
    /// where publish enforces topic naming.
    ///
    /// - [`Mode::Off`]: returns `Ok`; the reporter is not invoked.
    /// - [`Mode::Warn`]: invokes the reporter on failure, returns `Ok` so
    ///   the publish proceeds.
    /// - [`Mode::Strict`]: invokes the reporter on failure and returns
    ///   the error so the publish is aborted.
    fn check_topic(&self, t: &Topic) -> Result<(), BusError> {
        if self.enforce == Mode::Off {
            return Ok(());
        }
        let Err(e) = validate(t) else {
            return Ok(());
        };
        let err = BusError::InvalidTopic(e);
        if let Some(reporter) = &self.on_invalid {
            reporter(&err);
        }
        if self.enforce == Mode::Strict {
            return Err(err);
        }
        Ok(())
    }
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

/// A [`Bus`] wrapped for shared mutable access.
///
/// Subscribing needs `&mut Bus` while publishing needs `&Bus`, which is
/// awkward when a handler is registered from one place and events are
/// published from another. `SharedBus` hands out cheap clones over a
/// single `Arc<RwLock<Bus>>`, keeping the lock window to a single call.
///
/// `publish` holds the lock only while it snapshots the matching
/// handlers, and runs them with the lock released, so a handler may
/// publish, subscribe or unsubscribe on the same `SharedBus`. See the
/// [module docs](self#threads-and-re-entrancy).
#[derive(Clone, Default)]
pub struct SharedBus(Arc<RwLock<Bus>>);

impl SharedBus {
    /// Wraps `bus` for shared access.
    pub fn new(bus: Bus) -> Self {
        SharedBus(Arc::new(RwLock::new(bus)))
    }

    /// Registers a handler. See [`Bus::subscribe`].
    pub fn subscribe(
        &self,
        pattern: impl Into<String>,
        handler: impl Fn(&Event) -> Result<(), BusError> + Send + Sync + 'static,
    ) -> SubscriptionId {
        self.write().subscribe(pattern, handler)
    }

    /// Removes a subscription. See [`Bus::unsubscribe`].
    pub fn unsubscribe(&self, id: SubscriptionId) -> bool {
        self.write().unsubscribe(id)
    }

    /// Publishes an event. See [`Bus::publish`].
    ///
    /// The reporter and handlers run after the lock is released, so they
    /// may re-enter this `SharedBus`.
    pub fn publish(&self, event: &Event) -> Result<(), BusError> {
        let dispatch = self.read().dispatch(event);
        dispatch.run(event)
    }

    /// Closes the bus. See [`Bus::close`].
    pub fn close(&self) {
        self.write().close();
    }

    /// Reports whether the bus is closed.
    pub fn is_closed(&self) -> bool {
        self.read().is_closed()
    }

    // No handler or reporter runs under the lock, and every update to the
    // guarded `Bus` completes before user code can run, so a poisoned
    // lock never holds a half-applied change: recover the guard.
    fn read(&self) -> RwLockReadGuard<'_, Bus> {
        self.0.read().unwrap_or_else(PoisonError::into_inner)
    }

    fn write(&self) -> RwLockWriteGuard<'_, Bus> {
        self.0.write().unwrap_or_else(PoisonError::into_inner)
    }
}
