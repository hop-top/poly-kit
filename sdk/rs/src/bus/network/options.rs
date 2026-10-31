//! [`NetworkOptions`]: Go's `NetworkOption`s.

use std::fmt;
use std::sync::Arc;
use std::time::Duration;

use super::auth::Authenticator;
use super::error::NetworkError;
use super::filter::TopicFilter;
#[cfg(doc)]
use super::NetworkAdapter;

/// Options for a [`NetworkAdapter`]: Go's `NetworkOption`s. Later calls
/// win.
#[derive(Clone, Default)]
pub struct NetworkOptions {
    pub(crate) filter: TopicFilter,
    pub(crate) backoff: Option<(Duration, Duration)>,
    pub(crate) origin_id: Option<String>,
    pub(crate) auth: Option<Arc<dyn Authenticator>>,
    pub(crate) relay: bool,
    pub(crate) on_error: Option<ErrorFunc>,
    pub(crate) tls: Option<Arc<rustls::ClientConfig>>,
}

/// Receives errors the adapter cannot return to a caller.
pub(crate) type ErrorFunc = Arc<dyn Fn(&NetworkError) + Send + Sync>;

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

    /// Receives errors the adapter cannot return to a caller: Go's
    /// `WithNetworkErrorFunc`. On these links that is every failed
    /// handshake, including those of reconnect attempts, as
    /// [`NetworkError::Handshake`]. Runs on the blocking pool.
    pub fn error_func(mut self, f: impl Fn(&NetworkError) + Send + Sync + 'static) -> Self {
        self.on_error = Some(Arc::new(f));
        self
    }

    /// The TLS client config for `wss://` peers. Go has no such option:
    /// its dialer uses the platform's roots. By default this adapter does
    /// the same, through rustls on the ring provider with the platform's
    /// roots (plus the bundled Mozilla roots with feature
    /// `bus-network-webpki-roots`). Android has no platform store rustls
    /// can read: pass a config there, for example one built from
    /// `rustls-platform-verifier` or your own CA. The config's own crypto
    /// provider is used.
    pub fn tls_config(mut self, config: Arc<rustls::ClientConfig>) -> Self {
        self.tls = Some(config);
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
            .field("error_func", &self.on_error.is_some())
            .field("tls_config", &self.tls.is_some())
            .finish()
    }
}
