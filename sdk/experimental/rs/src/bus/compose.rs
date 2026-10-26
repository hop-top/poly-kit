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
//! [`compose`] runs them in that order. Step 2 needs the `bus-network`
//! feature; step 3 is a seam that returns the bus unchanged until its
//! layer is ported. Every layer is an [`Adapter`], so a new one wraps
//! the bus without changing this module's public API.
//!
//! [`DynBus`] is the front door, the analogue of Go's `New`. [`Bus`] and
//! [`SharedBus`] are the in-memory building block with none of these
//! layers.

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

/// The bus: Go's `Bus` interface value as `New` returns it, and the one
/// to reach for.
///
/// Built by [`DynBus::new`] or [`DynBus::builder`]. With no adapter it
/// runs on the in-memory [`SharedBus`]; the adapter, network and
/// env-sink layers stack on top as in Go. Cheap to clone; clones share
/// one bus. It is an [`Adapter`] itself, so it can sit under another
/// bus.
///
/// ```
/// use hop_top_kit::bus::{DynBus, Event, Mode};
/// use serde_json::json;
///
/// let bus = DynBus::builder().enforce(Mode::Strict).build();
/// bus.subscribe("crm.sales.deal.*", |e| {
///     println!("{} from {}", e.topic, e.source);
///     Ok(())
/// });
/// let event = Event::new("crm.sales.deal.created", "crm", json!({"id": 1}));
/// bus.publish(&event).unwrap();
/// bus.close().unwrap();
/// ```
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

/// Builds a [`DynBus`]: Go's `New` options, from [`DynBus::builder`].
/// Enforcement, reporter and pool size mean what they do on
/// [`BusBuilder`].
#[derive(Default)]
pub struct DynBusBuilder {
    base: BusBuilder,
    adapter: Option<Arc<dyn Adapter>>,
    network_addrs: Vec<String>,
    #[cfg(feature = "bus-network")]
    network_opts: super::network::NetworkOptions,
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

    /// Sets the transport adapter: Go's `WithAdapter`. The bus then
    /// enforces the topic mode, reports to the reporter and delegates to
    /// `adapter`; [`max_async`](Self::max_async) no longer applies, as in
    /// Go. The last call wins.
    pub fn with_adapter(mut self, adapter: impl Adapter + 'static) -> Self {
        self.adapter = Some(Arc::new(adapter));
        self
    }

    /// Attaches a network layer that connects to `addrs`: Go's
    /// `WithNetwork`. The last call wins; an empty list attaches none.
    ///
    /// [`build`](Self::build) then tries one connect per address, in
    /// order, and keeps retrying a failed one in the background; the
    /// layer closes the network adapter before the bus. Options come
    /// from `network_options` (feature `bus-network`).
    ///
    /// # Panics
    ///
    /// With any address when the crate is built without the
    /// `bus-network` feature: a bus that silently stayed local would pass
    /// for a networked one.
    pub fn with_network<I, S>(mut self, addrs: I) -> Self
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        self.network_addrs = addrs.into_iter().map(Into::into).collect();
        #[cfg(not(feature = "bus-network"))]
        assert!(self.network_addrs.is_empty(), "{NETWORK_UNAVAILABLE}");
        self
    }

    /// Options for the adapter [`with_network`](Self::with_network)
    /// attaches: Go's `WithNetworkOption`. The last call wins.
    #[cfg(feature = "bus-network")]
    pub fn network_options(mut self, opts: super::network::NetworkOptions) -> Self {
        self.network_opts = opts;
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
    #[cfg(feature = "bus-network")]
    let bus = network_layer(bus, &b.network_addrs, b.network_opts);
    #[cfg(not(feature = "bus-network"))]
    let bus = network_layer(bus, &b.network_addrs);
    env_sink_layer(bus, reporter.as_ref())
}

/// Panic text of [`DynBusBuilder::with_network`] without the
/// `bus-network` feature.
#[cfg(not(feature = "bus-network"))]
const NETWORK_UNAVAILABLE: &str =
    "bus: with_network: the network adapter needs the bus-network feature; build without addresses";

/// Step 2: with `addrs`, a network adapter over `bus` that has dialed
/// each address (Go's `networkedBus`); without, `bus` itself.
#[cfg(feature = "bus-network")]
fn network_layer(
    bus: Arc<dyn Adapter>,
    addrs: &[String],
    opts: super::network::NetworkOptions,
) -> Arc<dyn Adapter> {
    if addrs.is_empty() {
        return bus;
    }
    let network = super::network::NetworkAdapter::over(Arc::clone(&bus), opts);
    for addr in addrs {
        // Best effort; a failed first dial keeps retrying.
        network.dial_configured(addr);
    }
    Arc::new(NetworkedBus { bus, network })
}

/// Step 2 without the `bus-network` feature: `with_network` refuses
/// addresses, so there is no layer to add.
#[cfg(not(feature = "bus-network"))]
fn network_layer(bus: Arc<dyn Adapter>, addrs: &[String]) -> Arc<dyn Adapter> {
    let _ = addrs;
    bus
}

/// A bus with a network adapter attached: Go's `networkedBus`. Close
/// stops the network first, then the bus.
#[cfg(feature = "bus-network")]
struct NetworkedBus {
    bus: Arc<dyn Adapter>,
    network: super::network::NetworkAdapter,
}

#[cfg(feature = "bus-network")]
impl Adapter for NetworkedBus {
    fn publish(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        self.bus.publish(ctx, event)
    }
    fn subscribe(&self, pattern: &str, handler: Handler) -> SubscriptionId {
        self.bus.subscribe(pattern, handler)
    }
    fn subscribe_async(
        &self,
        pattern: &str,
        spawner: Arc<dyn Spawn>,
        handler: AsyncHandler,
    ) -> SubscriptionId {
        self.bus.subscribe_async(pattern, spawner, handler)
    }
    fn unsubscribe(&self, id: SubscriptionId) -> bool {
        self.bus.unsubscribe(id)
    }
    fn close(&self, ctx: &Context) -> Result<(), BusError> {
        // Go ignores the network's close error; this one never fails.
        let _ = self.network.close();
        self.bus.close(ctx)
    }
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

/// Lifecycle events from `serve` go onto the bus; a publish failure is
/// dropped, as that trait requires.
#[cfg(feature = "serve")]
impl crate::serve::Publisher for DynBus {
    fn publish(&self, topic: &str, source: &str, payload: &crate::serve::EventPayload) {
        let _ = DynBus::publish(self, &Event::new(topic, source, payload.to_json()));
    }
}
