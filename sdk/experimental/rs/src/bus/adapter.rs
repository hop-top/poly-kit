//! The transport seam: [`Adapter`] and [`MemoryAdapter`]. Mirrors Go
//! `go/runtime/bus/adapter.go`.
//!
//! An adapter delivers events and manages subscribers; delivery timing,
//! error propagation and ordering are adapter-specific, as in Go. Go's
//! `Adapter` and `Bus` interfaces have the same methods, so here one
//! trait serves both: every layer the builder composes (the adapter, the
//! network layer, the sink tee) is an `Adapter` wrapping another, and
//! [`DynBus`](super::DynBus), the composed bus, is one too.
//!
//! Go's `Subscribe` returns an `Unsubscribe` closure; this port returns a
//! [`SubscriptionId`] for [`Adapter::unsubscribe`], as [`Bus`] does.
//!
//! The SQLite adapter is not ported.

use std::future::Future;
use std::sync::Arc;

use super::async_dispatch::{MaybeSend, Spawn, Task};
use super::context::Context;
use super::event::Event;
use super::mem::{Bus, BusError, Handler, Mode, SubscriptionId};
use super::shared::{Kind, SharedBus};

/// An async handler as an [`Adapter`] takes it: called with the publish
/// context and a copy of the event, it returns the [`Task`] the
/// subscription's [`Spawn`] runs. Go's `AsyncHandler`. Build one from an
/// `async` closure with [`async_handler`].
pub type AsyncHandler = Arc<dyn Fn(Context, Event) -> Task + Send + Sync>;

/// Boxes `f` into an [`AsyncHandler`]; the bound matches
/// [`SharedBus::subscribe_async`].
pub fn async_handler<F, Fut>(f: F) -> AsyncHandler
where
    F: Fn(Context, Event) -> Fut + Send + Sync + 'static,
    Fut: Future<Output = ()> + MaybeSend + 'static,
{
    Arc::new(move |ctx, e| Box::pin(f(ctx, e)) as Task)
}

/// The transport layer for the bus: Go's `Adapter`. The default is
/// [`MemoryAdapter`].
pub trait Adapter: Send + Sync {
    /// Delivers `event`. Delivery timing and error propagation are
    /// adapter-specific.
    fn publish(&self, ctx: &Context, event: &Event) -> Result<(), BusError>;

    /// Registers a sync handler for `pattern`. Delivery guarantees
    /// depend on the adapter.
    fn subscribe(&self, pattern: &str, handler: Handler) -> SubscriptionId;

    /// Registers an async handler for `pattern`, run on tasks `spawner`
    /// executes. Delivery guarantees depend on the adapter.
    fn subscribe_async(
        &self,
        pattern: &str,
        spawner: Arc<dyn Spawn>,
        handler: AsyncHandler,
    ) -> SubscriptionId;

    /// Removes a subscription. Returns whether it was found. Go's
    /// `Unsubscribe` closure.
    fn unsubscribe(&self, id: SubscriptionId) -> bool;

    /// Stops the adapter and waits for in-flight handlers until `ctx` is
    /// done.
    fn close(&self, ctx: &Context) -> Result<(), BusError>;
}

/// Lets a caller hand the builder an `Arc` and keep a handle on the
/// adapter, as a Go caller keeps its pointer.
impl<A: Adapter + ?Sized> Adapter for Arc<A> {
    fn publish(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        (**self).publish(ctx, event)
    }
    fn subscribe(&self, pattern: &str, handler: Handler) -> SubscriptionId {
        (**self).subscribe(pattern, handler)
    }
    fn subscribe_async(
        &self,
        pattern: &str,
        spawner: Arc<dyn Spawn>,
        handler: AsyncHandler,
    ) -> SubscriptionId {
        (**self).subscribe_async(pattern, spawner, handler)
    }
    fn unsubscribe(&self, id: SubscriptionId) -> bool {
        (**self).unsubscribe(id)
    }
    fn close(&self, ctx: &Context) -> Result<(), BusError> {
        (**self).close(ctx)
    }
}

/// The in-memory bus as an adapter: its own topic enforcement, pool and
/// draining close. The base layer when no adapter is configured.
impl Adapter for SharedBus {
    fn publish(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        self.publish_ctx(ctx, event)
    }
    fn subscribe(&self, pattern: &str, handler: Handler) -> SubscriptionId {
        self.add(pattern.to_string(), Kind::Sync(Arc::from(handler)))
    }
    fn subscribe_async(
        &self,
        pattern: &str,
        spawner: Arc<dyn Spawn>,
        handler: AsyncHandler,
    ) -> SubscriptionId {
        self.add(pattern.to_string(), Kind::Async(spawner, handler))
    }
    fn unsubscribe(&self, id: SubscriptionId) -> bool {
        SharedBus::unsubscribe(self, id)
    }
    fn close(&self, ctx: &Context) -> Result<(), BusError> {
        self.close_ctx(ctx)
    }
}

/// An in-process adapter: Go's `MemoryAdapter`. Async handlers run on
/// the subscription's [`Spawn`], drawn from a pool of
/// [`DEFAULT_MAX_ASYNC`](super::DEFAULT_MAX_ASYNC) slots.
///
/// As in Go, the adapter does not validate topics (its mode is
/// [`Mode::Off`]); the bus that wraps it enforces the configured mode
/// before delegating. Cheap to clone; clones share subscriptions.
#[derive(Clone)]
pub struct MemoryAdapter(SharedBus);

impl Default for MemoryAdapter {
    fn default() -> Self {
        Self::new()
    }
}

impl MemoryAdapter {
    /// Go's `NewMemoryAdapter`.
    pub fn new() -> Self {
        MemoryAdapter(SharedBus::new(Bus::builder().enforce(Mode::Off).build()))
    }
}

impl Adapter for MemoryAdapter {
    fn publish(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        Adapter::publish(&self.0, ctx, event)
    }
    fn subscribe(&self, pattern: &str, handler: Handler) -> SubscriptionId {
        Adapter::subscribe(&self.0, pattern, handler)
    }
    fn subscribe_async(
        &self,
        pattern: &str,
        spawner: Arc<dyn Spawn>,
        handler: AsyncHandler,
    ) -> SubscriptionId {
        Adapter::subscribe_async(&self.0, pattern, spawner, handler)
    }
    fn unsubscribe(&self, id: SubscriptionId) -> bool {
        Adapter::unsubscribe(&self.0, id)
    }
    fn close(&self, ctx: &Context) -> Result<(), BusError> {
        Adapter::close(&self.0, ctx)
    }
}
