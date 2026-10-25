//! [`DynBus`] and its builder: Go's `New(opts...)` and the layers it
//! stacks.
//!
//! Go's `New` builds the bus in three steps, each wrapping the last:
//!
//! 1. **Base**: the configured adapter behind topic enforcement (Go's
//!    `adapterBus`), or, with none, the in-memory bus (Go's `memBus`)
//!    sized by `max_async`.
//! 2. **Network**: with addresses from [`DynBusBuilder::with_network`],
//!    a network layer that relays to peers (Go's `networkedBus`).
//! 3. **Env sinks**: with `KIT_BUS_SINK` set, a tee to the sinks it
//!    names, applied last so the sinks see every event publish
//!    accepted (Go's `TeeBus`).
//!
//! [`compose`] runs them in that order. Steps 2 and 3 are seams here:
//! each takes the bus built so far and returns it unchanged until its
//! layer is ported. Every layer is an [`Adapter`], so a new one wraps
//! the bus without changing this module's public API.
//!
//! [`Bus`] and [`BusBuilder::build`] stay the plain in-memory bus with
//! none of these layers; [`DynBus`] is the analogue of Go's `New`.

use std::future::Future;
use std::sync::Arc;

use super::adapter::{async_handler, Adapter, AsyncHandler};
use super::async_dispatch::{MaybeSend, Spawn};
use super::context::Context;
use super::event::Event;
#[cfg(doc)]
use super::mem::Bus;
use super::mem::{check_topic, BusBuilder, BusError, Handler, Mode, SharedErrFn, SubscriptionId};
use super::shared::SharedBus;

/// The composed bus: Go's `Bus` interface value as `New` returns it.
///
/// Built by [`DynBus::new`], [`DynBus::builder`], or a [`BusBuilder`]
/// given [`with_adapter`](BusBuilder::with_adapter) or
/// [`with_network`](BusBuilder::with_network). Cheap to clone; clones
/// share one bus. It is an [`Adapter`] itself, so it can sit under
/// another bus.
#[derive(Clone)]
pub struct DynBus(Arc<dyn Adapter>);

impl Default for DynBus {
    fn default() -> Self {
        Self::new()
    }
}

impl DynBus {
    /// Go's `New()`: the in-memory bus in [`Mode::Warn`], plus whatever
    /// layers the environment adds.
    pub fn new() -> Self {
        Self::builder().build()
    }

    /// Starts a [`DynBusBuilder`]: Go's `New` options.
    pub fn builder() -> DynBusBuilder {
        DynBusBuilder::default()
    }

    /// Registers a handler that ignores the publish context. See
    /// [`Bus::subscribe`].
    pub fn subscribe(
        &self,
        pattern: impl Into<String>,
        handler: impl Fn(&Event) -> Result<(), BusError> + Send + Sync + 'static,
    ) -> SubscriptionId {
        self.subscribe_ctx(pattern, move |_, e| handler(e))
    }

    /// Registers a handler that receives the publish context. Go's
    /// `Subscribe`.
    pub fn subscribe_ctx(
        &self,
        pattern: impl Into<String>,
        handler: impl Fn(&Context, &Event) -> Result<(), BusError> + Send + Sync + 'static,
    ) -> SubscriptionId {
        self.0.subscribe(&pattern.into(), Box::new(handler))
    }

    /// Registers an async handler run on tasks `spawner` executes. Go's
    /// `SubscribeAsync`; see [`SharedBus::subscribe_async`].
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
        self.0
            .subscribe_async(&pattern.into(), spawner, async_handler(handler))
    }

    /// Removes a subscription. Returns whether it was found.
    pub fn unsubscribe(&self, id: SubscriptionId) -> bool {
        self.0.unsubscribe(id)
    }

    /// [`DynBus::publish_ctx`] with [`Context::background`].
    pub fn publish(&self, event: &Event) -> Result<(), BusError> {
        self.publish_ctx(&Context::background(), event)
    }

    /// Publishes through every layer. Go's `Publish`.
    pub fn publish_ctx(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        self.0.publish(ctx, event)
    }

    /// [`DynBus::close_ctx`] with [`Context::background`].
    pub fn close(&self) -> Result<(), BusError> {
        self.close_ctx(&Context::background())
    }

    /// Closes every layer, outermost first, waiting for in-flight
    /// handlers until `ctx` is done. Go's `Close(ctx)`.
    pub fn close_ctx(&self, ctx: &Context) -> Result<(), BusError> {
        self.0.close(ctx)
    }
}

impl Adapter for DynBus {
    fn publish(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        self.0.publish(ctx, event)
    }
    fn subscribe(&self, pattern: &str, handler: Handler) -> SubscriptionId {
        self.0.subscribe(pattern, handler)
    }
    fn subscribe_async(
        &self,
        pattern: &str,
        spawner: Arc<dyn Spawn>,
        handler: AsyncHandler,
    ) -> SubscriptionId {
        self.0.subscribe_async(pattern, spawner, handler)
    }
    fn unsubscribe(&self, id: SubscriptionId) -> bool {
        self.0.unsubscribe(id)
    }
    fn close(&self, ctx: &Context) -> Result<(), BusError> {
        self.0.close(ctx)
    }
}

/// Builds a [`DynBus`]: Go's `New` options. Enforcement, reporter and
/// pool size mean what they do on [`BusBuilder`].
#[derive(Default)]
pub struct DynBusBuilder {
    base: BusBuilder,
    adapter: Option<Arc<dyn Adapter>>,
    network_addrs: Vec<String>,
}

impl BusBuilder {
    /// Sets the transport adapter: Go's `WithAdapter`. The bus then
    /// enforces the topic mode and delegates to `adapter`, and
    /// [`max_async`](BusBuilder::max_async) no longer applies, as in Go.
    /// Returns a [`DynBusBuilder`], whose `build` gives a [`DynBus`].
    pub fn with_adapter(self, adapter: impl Adapter + 'static) -> DynBusBuilder {
        DynBusBuilder::from(self).with_adapter(adapter)
    }

    /// Attaches the network layer: Go's `WithNetwork`. See
    /// [`DynBusBuilder::with_network`].
    pub fn with_network<I, S>(self, addrs: I) -> DynBusBuilder
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        DynBusBuilder::from(self).with_network(addrs)
    }
}

impl From<BusBuilder> for DynBusBuilder {
    fn from(base: BusBuilder) -> Self {
        DynBusBuilder {
            base,
            adapter: None,
            network_addrs: Vec::new(),
        }
    }
}

impl DynBusBuilder {
    /// See [`BusBuilder::enforce`].
    pub fn enforce(mut self, m: Mode) -> Self {
        self.base = self.base.enforce(m);
        self
    }

    /// See [`BusBuilder::invalid_topic_reporter`].
    pub fn invalid_topic_reporter(self, f: impl Fn(&BusError) + Send + Sync + 'static) -> Self {
        DynBusBuilder {
            base: self.base.invalid_topic_reporter(f),
            ..self
        }
    }

    /// See [`BusBuilder::max_async`]. Ignored with an adapter, as in Go.
    pub fn max_async(mut self, n: usize) -> Self {
        self.base = self.base.max_async(n);
        self
    }

    /// See [`BusBuilder::with_adapter`]. The last call wins.
    pub fn with_adapter(mut self, adapter: impl Adapter + 'static) -> Self {
        self.adapter = Some(Arc::new(adapter));
        self
    }

    /// Attaches a network layer that connects to `addrs`: Go's
    /// `WithNetwork`. The last call wins; an empty list attaches none.
    ///
    /// The network adapter is not ported yet: until it is, the
    /// addresses are recorded and the bus is built without the layer,
    /// delivering locally only.
    pub fn with_network<I, S>(mut self, addrs: I) -> Self
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        self.network_addrs = addrs.into_iter().map(Into::into).collect();
        self
    }

    /// Composes the bus in Go's `New` order, each layer wrapping the
    /// last: the adapter behind topic enforcement (with none, the
    /// in-memory bus), then the network layer, then the env sinks.
    pub fn build(self) -> DynBus {
        DynBus(compose(self))
    }
}

/// Go's `New`, after option parsing: base, then network, then env sinks.
fn compose(b: DynBusBuilder) -> Arc<dyn Adapter> {
    let reporter = b.base.on_invalid.clone();
    let bus: Arc<dyn Adapter> = match b.adapter {
        Some(adapter) => Arc::new(AdapterLayer {
            adapter,
            enforce: b.base.enforce.unwrap_or_default(),
            on_invalid: reporter.clone(),
        }),
        None => Arc::new(SharedBus::new(b.base.build())),
    };
    let bus = network_layer(bus, &b.network_addrs);
    env_sink_layer(bus, reporter.as_ref())
}

/// Step 2. Seam for the network adapter: with `addrs` non-empty it will
/// attach a network adapter to `bus`, dial each address (a failed first
/// dial keeps retrying) and return a layer that closes the network
/// before `bus`. Until then it returns `bus`.
fn network_layer(bus: Arc<dyn Adapter>, addrs: &[String]) -> Arc<dyn Adapter> {
    let _ = addrs;
    bus
}

/// Step 3. Seam for the env-driven sinks (`KIT_BUS_SINK`,
/// `KIT_BUS_SINK_PATH`): with sinks configured it will wrap `bus` in a
/// tee reporting sink errors to `reporter`. Until then it returns `bus`.
fn env_sink_layer(bus: Arc<dyn Adapter>, reporter: Option<&SharedErrFn>) -> Arc<dyn Adapter> {
    let _ = reporter;
    bus
}

/// An adapter behind topic enforcement: Go's `adapterBus`.
struct AdapterLayer {
    adapter: Arc<dyn Adapter>,
    enforce: Mode,
    on_invalid: Option<SharedErrFn>,
}

impl Adapter for AdapterLayer {
    fn publish(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        check_topic(self.enforce, self.on_invalid.as_ref(), &event.topic)?;
        self.adapter.publish(ctx, event)
    }
    fn subscribe(&self, pattern: &str, handler: Handler) -> SubscriptionId {
        self.adapter.subscribe(pattern, handler)
    }
    fn subscribe_async(
        &self,
        pattern: &str,
        spawner: Arc<dyn Spawn>,
        handler: AsyncHandler,
    ) -> SubscriptionId {
        self.adapter.subscribe_async(pattern, spawner, handler)
    }
    fn unsubscribe(&self, id: SubscriptionId) -> bool {
        self.adapter.unsubscribe(id)
    }
    fn close(&self, ctx: &Context) -> Result<(), BusError> {
        self.adapter.close(ctx)
    }
}
