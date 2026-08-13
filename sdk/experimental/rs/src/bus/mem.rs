//! In-memory pub/sub dispatch.
//!
//! # Dispatch model: synchronous, single-threaded
//!
//! The Go canonical runtime (`go/runtime/bus/bus.go`) splits subscribers
//! into two kinds. Sync handlers run inline and may veto the publish;
//! async handlers run on goroutines drawn from a bounded semaphore pool,
//! with a `WaitGroup` drained by `Close` under a caller deadline. That
//! machinery buys concurrent fan-out in long-lived multi-threaded server
//! processes.
//!
//! This port ships **synchronous dispatch only**, and it is a decision,
//! not an omission:
//!
//! - No current Rust consumer needs concurrent delivery. The first one is
//!   a single-user CLI that publishes a handful of events per invocation
//!   and exits; handlers run in microseconds and never block on I/O.
//! - A faithful port would make `tokio` a hard dependency of the `bus`
//!   feature and reintroduce, in a language with different aliasing
//!   rules, the exact races the Go code documents at length: the
//!   RLock/semaphore priority inversion, the `wg.Add`-after-acquire
//!   ordering, the closed-flag recheck between slot acquisition and
//!   spawn. That is a large surface to carry, and to keep correct, for
//!   behaviour nobody exercises.
//! - Synchronous delivery is a strict subset of the async contract from
//!   the subscriber's point of view: a handler that would have run on a
//!   pool thread instead runs on the publisher's thread. Handlers that
//!   want to defer work can spawn a task themselves.
//!
//! Consequence to be aware of: a slow handler blocks the publisher, and
//! [`Bus::publish`] returns only once every matching subscriber has run.
//! If a Rust consumer ever genuinely needs non-blocking fan-out, the
//! honest move is to add an explicit async subscribe path backed by
//! tokio behind its own feature — not to bolt threads onto this one.
//!
//! Everything else is behaviour-preserving against Go: subscription
//! identity and removal, first-error veto semantics, publish-after-close
//! rejection, and the [`Mode`] enforcement ladder.

use std::cell::RefCell;
use std::fmt;
use std::rc::Rc;

use super::event::Event;
use super::topic::{validate, InvalidTopicError, Topic};

/// A synchronous event handler. Returning an error vetoes the publish —
/// no further handler runs for that event, and the error surfaces to the
/// publisher.
pub type Handler = Box<dyn Fn(&Event) -> Result<(), BusError>>;

/// Receives errors from non-critical operations (e.g. topic-naming
/// violations in [`Mode::Warn`]).
pub type ErrFn = Box<dyn Fn(&BusError)>;

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
    handler: Handler,
}

/// In-memory publish/subscribe bus.
///
/// See the [module docs](self) for why dispatch is synchronous.
pub struct Bus {
    subs: Vec<Subscription>,
    next_id: u64,
    closed: bool,
    enforce: Mode,
    on_invalid: Option<ErrFn>,
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
        handler: impl Fn(&Event) -> Result<(), BusError> + 'static,
    ) -> SubscriptionId {
        let id = SubscriptionId(self.next_id);
        self.next_id += 1;
        self.subs.push(Subscription {
            id,
            pattern: pattern.into(),
            handler: Box::new(handler),
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
        self.check_topic(&event.topic)?;
        if self.closed {
            return Err(BusError::Closed);
        }
        for s in &self.subs {
            if event.topic.matches(&s.pattern) {
                (s.handler)(event)?;
            }
        }
        Ok(())
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
    on_invalid: Option<ErrFn>,
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
    pub fn invalid_topic_reporter(mut self, f: impl Fn(&BusError) + 'static) -> Self {
        self.on_invalid = Some(Box::new(f));
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
/// single `RefCell`, keeping the borrow window to a single call.
///
/// Single-threaded by construction (`Rc`/`RefCell`), matching the
/// synchronous dispatch model described in the [module docs](self).
#[derive(Clone, Default)]
pub struct SharedBus(Rc<RefCell<Bus>>);

impl SharedBus {
    /// Wraps `bus` for shared access.
    pub fn new(bus: Bus) -> Self {
        SharedBus(Rc::new(RefCell::new(bus)))
    }

    /// Registers a handler. See [`Bus::subscribe`].
    pub fn subscribe(
        &self,
        pattern: impl Into<String>,
        handler: impl Fn(&Event) -> Result<(), BusError> + 'static,
    ) -> SubscriptionId {
        self.0.borrow_mut().subscribe(pattern, handler)
    }

    /// Removes a subscription. See [`Bus::unsubscribe`].
    pub fn unsubscribe(&self, id: SubscriptionId) -> bool {
        self.0.borrow_mut().unsubscribe(id)
    }

    /// Publishes an event. See [`Bus::publish`].
    ///
    /// The inner borrow is held for the whole dispatch, so a handler must
    /// not publish re-entrantly on the same `SharedBus`.
    pub fn publish(&self, event: &Event) -> Result<(), BusError> {
        self.0.borrow().publish(event)
    }

    /// Closes the bus. See [`Bus::close`].
    pub fn close(&self) {
        self.0.borrow_mut().close();
    }

    /// Reports whether the bus is closed.
    pub fn is_closed(&self) -> bool {
        self.0.borrow().is_closed()
    }
}
