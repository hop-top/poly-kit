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

pub use super::async_dispatch::{async_handler, AsyncHandler};
use super::async_dispatch::{MaybeSend, Spawn};
use super::context::Context;
use super::event::Event;
use super::mem::{Bus, BusError, Handler, Mode, SubscriptionId};
use super::shared::{Kind, SharedBus};

/// The transport layer for the bus: Go's `Adapter`. The default is
/// [`MemoryAdapter`].
///
/// Adapters are `Send + Sync`, as the bus is, on every target. A future
/// browser adapter holding a `JsValue` would need that bound relaxed on
/// wasm32 (as [`MaybeSend`] does for task futures), which changes this
/// trait.
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

/// Lets a caller hand the builder a shared `Arc<dyn Adapter>` and keep a
/// handle on it, as a Go caller keeps its pointer. Concrete adapters are
/// cheap clones sharing state ([`MemoryAdapter`], [`DynBus`]), so a clone
/// serves the same purpose.
///
/// [`DynBus`]: super::DynBus
impl Adapter for Arc<dyn Adapter> {
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

    /// Registers a handler that ignores the publish context. Go's
    /// `Subscribe`; see [`SharedBus::subscribe`].
    pub fn subscribe(
        &self,
        pattern: impl Into<String>,
        handler: impl Fn(&Event) -> Result<(), BusError> + Send + Sync + 'static,
    ) -> SubscriptionId {
        self.0.subscribe(pattern, handler)
    }

    /// Registers a handler that receives the publish context.
    pub fn subscribe_ctx(
        &self,
        pattern: impl Into<String>,
        handler: impl Fn(&Context, &Event) -> Result<(), BusError> + Send + Sync + 'static,
    ) -> SubscriptionId {
        self.0.subscribe_ctx(pattern, handler)
    }

    /// Registers an async handler. Go's `SubscribeAsync`; see
    /// [`SharedBus::subscribe_async`].
    pub fn subscribe_async<F, Fut>(
        &self,
        pattern: impl Into<String>,
        spawner: Arc<dyn Spawn>,
        handler: F,
    ) -> SubscriptionId
    where
        F: Fn(Context, Event) -> Fut + Send + Sync + 'static,
        Fut: Future<Output = ()> + MaybeSend + 'static,
    {
        self.0.subscribe_async(pattern, spawner, handler)
    }

    /// Removes a subscription. Returns whether it was found.
    pub fn unsubscribe(&self, id: SubscriptionId) -> bool {
        self.0.unsubscribe(id)
    }

    /// [`MemoryAdapter::publish_ctx`] with [`Context::background`].
    pub fn publish(&self, event: &Event) -> Result<(), BusError> {
        self.0.publish(event)
    }

    /// Delivers `event` to the matching handlers. Go's `Publish`.
    pub fn publish_ctx(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        self.0.publish_ctx(ctx, event)
    }

    /// Stops the adapter and waits for in-flight handlers until `ctx` is
    /// done. Go's `Close`.
    pub fn close_ctx(&self, ctx: &Context) -> Result<(), BusError> {
        self.0.close_ctx(ctx)
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
