//! [`NetworkAdapter`] and its options: Go's `network.go` and
//! `network_session.go` on legacy links.

use std::collections::HashMap;
use std::fmt;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex, MutexGuard, PoisonError};
use std::time::Duration;

use super::auth::Authenticator;
use super::conn::{Link, STATUS_GOING_AWAY, STATUS_NORMAL};
use super::error::NetworkError;
use super::filter::TopicFilter;
use super::runtime::{self, TokioSpawn};
use super::sync::{Signal, WaitGroup};
use crate::bus::adapter::{async_handler, Adapter};
use crate::bus::context::Context;
use crate::bus::mem::SubscriptionId;
use crate::bus::stream::LinkState;
use crate::netpolicy::NetPolicy;
use crate::util::retry::RetryConfig;

/// Options for a [`NetworkAdapter`]: Go's `NetworkOption`s. Later calls
/// win.
#[derive(Clone, Default)]
pub struct NetworkOptions {
    pub(crate) filter: TopicFilter,
    pub(crate) backoff: Option<(Duration, Duration)>,
    pub(crate) origin_id: Option<String>,
    pub(crate) auth: Option<Arc<dyn Authenticator>>,
    pub(crate) relay: bool,
}

impl NetworkOptions {
    /// The defaults: forward everything, 100 ms to 30 s jittered
    /// reconnect backoff, a random origin id, no auth, no relay.
    pub fn new() -> Self {
        Self::default()
    }

    /// Which topics are forwarded to peers: Go's `WithFilter`.
    pub fn filter(mut self, f: TopicFilter) -> Self {
        self.filter = f;
        self
    }

    /// The reconnect backoff's base delay and cap: Go's `WithBackoff`.
    pub fn backoff(mut self, base: Duration, cap: Duration) -> Self {
        self.backoff = Some((base, cap));
        self
    }

    /// The origin id stamped on forwarded events for loop prevention:
    /// Go's `WithOriginID`. Defaults to 8 random bytes in hex.
    pub fn origin_id(mut self, id: impl Into<String>) -> Self {
        self.origin_id = Some(id.into());
        self
    }

    /// The handshake credentials for both directions: Go's `WithAuth`.
    pub fn auth(mut self, a: impl Authenticator + 'static) -> Self {
        self.auth = Some(Arc::new(a));
        self
    }

    /// Star-topology relay: Go's `WithRelay`. Inbound network events are
    /// forwarded again to every peer except the one they came from. Off
    /// by default; turn it on for a hub bridging several peers.
    pub fn relay(mut self, enabled: bool) -> Self {
        self.relay = enabled;
        self
    }
}

impl fmt::Debug for NetworkOptions {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("NetworkOptions")
            .field("filter", &self.filter)
            .field("backoff", &self.backoff)
            .field("origin_id", &self.origin_id)
            .field("auth", &self.auth.is_some())
            .field("relay", &self.relay)
            .finish()
    }
}

/// The context value marking an event the adapter injected from the
/// network, carrying its wire origin: Go's `networkOriginKey`.
#[derive(Clone, Debug)]
pub(crate) struct NetworkOrigin(pub(crate) String);

/// The state the adapter's lock guards.
#[derive(Default)]
pub(crate) struct State {
    /// Live links by address: dialed URLs and accepted remote addresses.
    pub(crate) conns: HashMap<String, Arc<Link>>,
    /// Registered outbound addresses: Go's `peers`. Disconnect forgets
    /// one; a reconnect never re-registers it.
    pub(crate) peers: HashMap<String, LinkState>,
    /// Addresses with a reconnect loop running.
    pub(crate) reconnecting: std::collections::HashSet<String>,
}

/// A test seam: fails a forward write when it returns `true`.
pub(crate) type WriteFault = Arc<dyn Fn(&str) -> bool + Send + Sync>;

pub(crate) struct Inner {
    pub(crate) bus: Arc<dyn Adapter>,
    pub(crate) origin_id: String,
    pub(crate) filter: TopicFilter,
    pub(crate) reconnect: RetryConfig,
    pub(crate) auth: Option<Arc<dyn Authenticator>>,
    pub(crate) relay: bool,
    state: Mutex<State>,
    pub(crate) closed: AtomicBool,
    /// Fired by close: stops listeners and reconnect loops.
    pub(crate) closing: Signal,
    offline: AtomicBool,
    sub: Mutex<Option<SubscriptionId>>,
    pub(crate) tasks: WaitGroup,
    pub(crate) write_fault: Mutex<Option<WriteFault>>,
    /// Go's `handshakeTimeout`; a test seam shortens it.
    pub(crate) handshake_timeout: Mutex<Duration>,
}

impl Inner {
    pub(crate) fn state(&self) -> MutexGuard<'_, State> {
        // Every critical section is a map update; none runs user code.
        self.state.lock().unwrap_or_else(PoisonError::into_inner)
    }

    pub(crate) fn handshake_timeout(&self) -> Duration {
        *self
            .handshake_timeout
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
    }

    /// Records a policy seen on a connect. Sticky: once a run is
    /// offline, a later online connect does not clear it for peers dialed
    /// under the offline policy.
    pub(crate) fn learn_policy(&self, ctx: &Context) {
        if is_offline(ctx) {
            self.offline.store(true, Ordering::SeqCst);
        }
    }

    /// The root context of adapter-owned work: Go's `baseCtx`. Reconnects
    /// outlive the caller's context, so they start here, carrying the
    /// offline policy a connect recorded.
    pub(crate) fn base_ctx(&self) -> Context {
        if self.offline.load(Ordering::SeqCst) {
            Context::background().with_value(NetPolicy::offline())
        } else {
            Context::background()
        }
    }
}

/// The policy a context carries: offline when it holds an offline
/// [`NetPolicy`], as Go's `netpolicy.IsOffline(ctx)`.
pub(crate) fn policy(ctx: &Context) -> NetPolicy {
    ctx.value::<NetPolicy>().copied().unwrap_or_default()
}

fn is_offline(ctx: &Context) -> bool {
    policy(ctx).is_offline()
}

/// Bridges a bus to remote peers over WebSocket: Go's `NetworkAdapter`.
///
/// Events published on the bus are forwarded to every connected peer
/// (subject to the [`TopicFilter`]) by an async subscriber on `#`; events
/// read from a peer are published on the bus with a network-origin
/// marker, so they are not forwarded again unless relay is on. Each
/// forwarded frame carries the origin id of the adapter that first
/// published it, and a frame carrying this adapter's own id is dropped:
/// a loop of peers never echoes.
///
/// Links are made by [`connect`](Self::connect) (outbound, reconnected
/// with backoff when they drop) and accepted by [`listen`](Self::listen),
/// [`accept_upgraded`](Self::accept_upgraded) or the axum handler.
///
/// The I/O runs on the crate's network runtime, a dedicated thread, so
/// no async runtime is needed. The blocking methods ([`connect`],
/// [`disconnect`], [`close`]) must not be called from that runtime: from
/// async code use the `_async` variants.
///
/// Cheap to clone; clones share one adapter. Call [`close`] when done:
/// it stops forwarding and closes every link.
///
/// [`connect`]: Self::connect
/// [`disconnect`]: Self::disconnect
/// [`close`]: Self::close
#[derive(Clone)]
pub struct NetworkAdapter(pub(crate) Arc<Inner>);

impl NetworkAdapter {
    /// Bridges `bus`: Go's `NewNetworkAdapter`. Subscribes the outbound
    /// forwarder at once; connect or accept peers next. A [`DynBus`]
    /// clone shares the bus it came from.
    ///
    /// [`DynBus`]: crate::bus::DynBus
    pub fn new(bus: impl Adapter + 'static, opts: NetworkOptions) -> Self {
        Self::over(Arc::new(bus), opts)
    }

    /// [`NetworkAdapter::new`] over a shared adapter.
    pub(crate) fn over(bus: Arc<dyn Adapter>, opts: NetworkOptions) -> Self {
        let mut reconnect = RetryConfig::with_backoff();
        reconnect.base_delay = Duration::from_millis(100);
        reconnect.max_delay = Duration::from_secs(30);
        if let Some((base, cap)) = opts.backoff {
            reconnect.base_delay = base;
            reconnect.max_delay = cap;
        }
        let inner = Arc::new(Inner {
            bus,
            origin_id: opts.origin_id.unwrap_or_else(random_id),
            filter: opts.filter,
            reconnect,
            auth: opts.auth,
            relay: opts.relay,
            state: Mutex::new(State::default()),
            closed: AtomicBool::new(false),
            closing: Signal::default(),
            offline: AtomicBool::new(false),
            sub: Mutex::new(None),
            tasks: WaitGroup::default(),
            write_fault: Mutex::new(None),
            handshake_timeout: Mutex::new(super::session::HANDSHAKE_TIMEOUT),
        });
        let weak = Arc::downgrade(&inner);
        let id = inner.bus.subscribe_async(
            "#",
            Arc::new(TokioSpawn::new(runtime::handle())),
            async_handler(move |ctx, e| {
                let inner = weak.upgrade();
                async move {
                    if let Some(inner) = inner {
                        inner.observe(ctx, e).await;
                    }
                }
            }),
        );
        *inner.sub.lock().unwrap_or_else(PoisonError::into_inner) = Some(id);
        NetworkAdapter(inner)
    }

    /// The origin id stamped on events this adapter forwards.
    pub fn origin_id(&self) -> &str {
        &self.0.origin_id
    }

    /// The state of an outbound address; `None` unless it is registered
    /// (by a connect that reached the peer, [`dial`](Self::dial) or
    /// `with_network`) and not since disconnected. Go's `LinkState`.
    /// Only `connected` and, after a refused handshake, `code` (`auth`
    /// or `timeout`) are set on these links.
    pub fn link_state(&self, addr: &str) -> Option<LinkState> {
        self.0.state().peers.get(addr).cloned()
    }

    /// Dials `addr` and registers it as an outbound peer: Go's `Connect`.
    /// Blocks until the link is up or the attempt failed.
    ///
    /// `ctx` bounds the dial and the handshake. When it carries an
    /// offline [`NetPolicy`] (`ctx.with_value(NetPolicy::offline())`),
    /// a non-loopback peer is refused before any socket opens
    /// ([`NetworkError::Offline`]), and every later reconnect replays
    /// that policy.
    ///
    /// With an authenticator, the token goes out as the first frame and
    /// the peer must ack within 10 s; a peer that closes with 1008, or
    /// acks anything but `auth_ok`, fails with
    /// [`NetworkError::AuthFailed`]. A failed connect starts no
    /// reconnect loop; use [`dial`](Self::dial) to keep trying. A connect
    /// to an address already linked returns `Ok` without a second link.
    ///
    /// # Panics
    ///
    /// When called on the network runtime; use
    /// [`connect_async`](Self::connect_async) there.
    pub fn connect(&self, ctx: &Context, addr: &str) -> Result<(), NetworkError> {
        let inner = Arc::clone(&self.0);
        let (ctx, addr) = (ctx.clone(), addr.to_string());
        runtime::block_on(
            "connect",
            async move { inner.connect(ctx, addr, false).await },
        )
    }

    /// [`connect`](Self::connect) from async code, on any executor.
    pub async fn connect_async(&self, ctx: &Context, addr: &str) -> Result<(), NetworkError> {
        let inner = Arc::clone(&self.0);
        let (ctx, addr) = (ctx.clone(), addr.to_string());
        runtime::run(async move { inner.connect(ctx, addr, false).await }).await
    }

    /// Registers `addr` as an outbound peer and connects in the
    /// background, retrying with backoff until it succeeds, a disconnect,
    /// or close: Go's `Dial`.
    pub fn dial(&self, addr: &str) {
        self.0.register(addr);
        self.0.go_reconnect(addr);
    }

    /// Registers `addr`, tries one connect, and keeps retrying in the
    /// background if it fails: what `with_network` does per address.
    pub(crate) fn dial_configured(&self, addr: &str) {
        self.0.register(addr);
        let inner = Arc::clone(&self.0);
        let a = addr.to_string();
        let first = runtime::block_on("with_network", async move {
            inner.connect(Context::background(), a, true).await
        });
        if first.is_err() {
            self.0.go_reconnect(addr);
        }
    }

    /// Closes the link to `addr` and forgets the address, so it is not
    /// reconnected: Go's `Disconnect`. Waits for the link's reader. An
    /// unknown address is not an error.
    ///
    /// # Panics
    ///
    /// When called on the network runtime.
    pub fn disconnect(&self, addr: &str) -> Result<(), NetworkError> {
        runtime::assert_off_network_thread("disconnect");
        if let Some(link) = self.0.forget(addr) {
            link.done.wait_blocking();
        }
        Ok(())
    }

    /// [`disconnect`](Self::disconnect) from async code.
    pub async fn disconnect_async(&self, addr: &str) -> Result<(), NetworkError> {
        if let Some(link) = self.0.forget(addr) {
            link.done.wait().await;
        }
        Ok(())
    }

    /// Stops forwarding and closes every link with 1001 (going away),
    /// then waits for the readers, reconnect loops and listeners: Go's
    /// `Close`. Idempotent. Connects after close fail with
    /// [`NetworkError::Closed`]. Does not close the bus.
    ///
    /// # Panics
    ///
    /// When called on the network runtime.
    pub fn close(&self) -> Result<(), NetworkError> {
        runtime::assert_off_network_thread("close");
        if self.0.shut() {
            self.0.tasks.wait_blocking();
        }
        Ok(())
    }

    /// [`close`](Self::close) from async code.
    pub async fn close_async(&self) -> Result<(), NetworkError> {
        if self.0.shut() {
            self.0.tasks.wait().await;
        }
        Ok(())
    }
}

impl fmt::Debug for NetworkAdapter {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("NetworkAdapter")
            .field("origin_id", &self.0.origin_id)
            .field("closed", &self.0.closed.load(Ordering::SeqCst))
            .finish_non_exhaustive()
    }
}

impl Inner {
    /// Registers `addr` as an outbound peer: Go's `peerFor`.
    pub(crate) fn register(&self, addr: &str) {
        self.state().peers.entry(addr.to_string()).or_default();
    }

    /// Forgets `addr` and closes its link; returns the link to wait on.
    fn forget(&self, addr: &str) -> Option<Arc<Link>> {
        let link = {
            let mut st = self.state();
            st.peers.remove(addr);
            st.conns.get(addr).cloned()
        }?;
        link.close(STATUS_NORMAL, "disconnect");
        Some(link)
    }

    /// Starts closing: returns false when already closed.
    fn shut(&self) -> bool {
        if self.closed.swap(true, Ordering::SeqCst) {
            return false;
        }
        if let Some(id) = self
            .sub
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .take()
        {
            self.bus.unsubscribe(id);
        }
        self.closing.fire();
        let links: Vec<_> = self.state().conns.values().cloned().collect();
        for l in links {
            l.close(STATUS_GOING_AWAY, "closing");
        }
        true
    }
}

/// 8 random bytes in hex: Go's `randomID`.
fn random_id() -> String {
    let mut b = [0u8; 8];
    getrandom::fill(&mut b).expect("bus: network: no random source for the origin id");
    b.iter().map(|x| format!("{x:02x}")).collect()
}
