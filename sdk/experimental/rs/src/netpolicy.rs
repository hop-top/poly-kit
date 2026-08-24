//! Network policy marker and the client that enforces it.
//!
//! The `--offline` global (cli-parity-guide, "Global Flags") promises
//! that network access is disabled. Carrying that promise as a plain
//! flag value cannot keep it: it is advisory, so any caller that forgets
//! to consult it still reaches the wire. [`GuardedClient`] closes that
//! gap by refusing the request at the point where it is sent, beneath
//! every caller, where none can route around it.
//!
//! # Why this shape
//!
//! The Go side installs an `http.RoundTripper` over the process-global
//! `http.DefaultTransport`, so a bare `&http.Client{}` inherits the
//! policy without a per-site change. Rust has no such process-global:
//! `reqwest::Client::new()` builds an independent stack every time, and
//! nothing reqwest 0.12 exposes lets a policy see a request before it is
//! sent. Its `connector_layer` hook receives an opaque `Unnameable` (no
//! destination to inspect) and fires only on new connections, so pooled
//! reuse would slip past; a custom `dns_resolver` never sees IP-literal
//! URLs at all and cannot surface a typed error to the caller.
//!
//! So the chokepoint here is construction rather than transport: this
//! module owns the port's only `reqwest::Client` construction path, and
//! hands back a [`GuardedClient`] that does not expose the inner
//! `reqwest::Client`. Every request necessarily passes through
//! [`GuardedClient::execute`], which is where the policy is applied.
//!
//! # Marker
//!
//! Rust has no ambient request-scoped context, so the marker is an
//! explicit [`NetPolicy`] value threaded at client construction rather
//! than a context value. [`NetPolicy::default`] is online, so a port
//! that has not wired the flag behaves exactly as it did before.
//!
//! Loopback is deliberately exempt. `--offline` means "do not talk to
//! the network", not "do not talk to myself": a local peer, a dev
//! backend on 127.0.0.1 and unix sockets stay reachable so offline
//! workflows remain usable.
//!
//! # Scope
//!
//! [`GuardedClient`] covers HTTP and HTTPS through reqwest — every
//! network client in this port today. It does NOT cover code that opens
//! a socket directly: raw `TcpStream`, SQL drivers, gRPC or raw TLS. For
//! those, `--offline` remains advisory and the call site must consult
//! [`NetPolicy::is_offline`] itself. It also cannot reach a
//! `reqwest::Client` a consumer builds on its own, outside this module —
//! that is the price of Rust having no default transport to install
//! over.

use std::net::IpAddr;

use thiserror::Error;

/// The network policy marker. Threaded at client construction; the
/// analogue of the Go side's context value.
///
/// Default is online, so an unwired port keeps its current behaviour.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct NetPolicy {
    offline: bool,
}

impl NetPolicy {
    /// Build a policy from a boolean, mirroring Go's
    /// `WithOffline(ctx, bool)`. This is the one-liner the CLI layer
    /// calls once it registers the `--offline` flag.
    pub fn new(offline: bool) -> Self {
        Self { offline }
    }

    /// A policy that permits network access.
    pub fn online() -> Self {
        Self { offline: false }
    }

    /// A policy that refuses non-loopback network access.
    pub fn offline() -> Self {
        Self { offline: true }
    }

    /// Whether this policy refuses non-loopback network access.
    pub fn is_offline(&self) -> bool {
        self.offline
    }

    /// Whether a request to `url` is permitted under this policy.
    /// Loopback destinations are always permitted.
    pub fn allows(&self, url: &reqwest::Url) -> bool {
        !self.offline || url.host_str().is_some_and(is_loopback)
    }
}

/// The typed refusal, the analogue of Go's `ErrOffline`. Callers match
/// it with [`NetError::as_offline`] or by downcasting.
#[derive(Debug, Clone, Error, PartialEq, Eq)]
#[error("{method} {url}: network disabled by --offline")]
pub struct OfflineError {
    /// HTTP method of the refused request.
    pub method: String,
    /// Destination of the refused request, with any userinfo redacted.
    pub url: String,
}

/// Errors surfaced by [`GuardedClient`]. Refusals are a distinct,
/// matchable variant; everything else is reqwest's own error.
#[derive(Debug, Error)]
pub enum NetError {
    /// The request was refused by the offline policy.
    #[error(transparent)]
    Offline(#[from] OfflineError),
    /// The request was attempted and reqwest failed it.
    #[error(transparent)]
    Transport(#[from] reqwest::Error),
}

impl NetError {
    /// The offline refusal, if this error is one. The analogue of
    /// `errors.Is(err, netpolicy.ErrOffline)`.
    pub fn as_offline(&self) -> Option<&OfflineError> {
        match self {
            NetError::Offline(e) => Some(e),
            NetError::Transport(_) => None,
        }
    }

    /// Whether this error is an offline refusal.
    pub fn is_offline(&self) -> bool {
        self.as_offline().is_some()
    }
}

/// Whether `host` names a loopback address.
///
/// Hosts that are not literal IPs (DNS names) are treated as remote:
/// resolving them would itself be network access. Matches Go's
/// `isLoopback`, including the bare `localhost` special case and the
/// whole 127.0.0.0/8 range.
pub fn is_loopback(host: &str) -> bool {
    if host.eq_ignore_ascii_case("localhost") {
        return true;
    }
    // reqwest::Url::host_str keeps IPv6 literals bracketed; strip them
    // so the address parses.
    let bare = host
        .strip_prefix('[')
        .and_then(|h| h.strip_suffix(']'))
        .unwrap_or(host);
    bare.parse::<IpAddr>().is_ok_and(|ip| ip.is_loopback())
}

/// The port's shared HTTP client: the single construction path through
/// which every reqwest request in this crate is issued.
///
/// It deliberately does not expose its inner `reqwest::Client`. Handing
/// one out would let a caller send a request that never reaches
/// [`GuardedClient::execute`], which is the only place the policy is
/// applied.
#[derive(Debug, Clone)]
pub struct GuardedClient {
    inner: reqwest::Client,
    policy: NetPolicy,
}

impl GuardedClient {
    /// Build a client under `policy` with reqwest's defaults.
    pub fn new(policy: NetPolicy) -> Result<Self, reqwest::Error> {
        Self::build(policy, reqwest::Client::builder())
    }

    /// Build a client under `policy` from a configured
    /// `reqwest::ClientBuilder`, for callers that need timeouts or other
    /// transport settings. The policy is applied regardless of how the
    /// builder was configured.
    pub fn build(
        policy: NetPolicy,
        builder: reqwest::ClientBuilder,
    ) -> Result<Self, reqwest::Error> {
        Ok(Self {
            inner: builder.build()?,
            policy,
        })
    }

    /// The policy this client enforces.
    pub fn policy(&self) -> NetPolicy {
        self.policy
    }

    /// Start a request. The returned builder is reqwest's own, so the
    /// full builder surface stays available; it must be handed back to
    /// [`GuardedClient::send`] (or [`GuardedClient::execute`]) to be
    /// issued.
    pub fn request(&self, method: reqwest::Method, url: impl reqwest::IntoUrl) -> RequestBuilder {
        RequestBuilder {
            client: self.clone(),
            inner: self.inner.request(method, url),
        }
    }

    /// Start a GET request.
    pub fn get(&self, url: impl reqwest::IntoUrl) -> RequestBuilder {
        self.request(reqwest::Method::GET, url)
    }

    /// Start a POST request.
    pub fn post(&self, url: impl reqwest::IntoUrl) -> RequestBuilder {
        self.request(reqwest::Method::POST, url)
    }

    /// Issue `req`, refusing it when the policy forbids the
    /// destination. This is the chokepoint: no request leaves this
    /// crate without passing through here.
    pub async fn execute(&self, req: reqwest::Request) -> Result<reqwest::Response, NetError> {
        if !self.policy.allows(req.url()) {
            let mut url = req.url().clone();
            // Match Go's url.Redacted(): never echo credentials back in
            // an error message.
            if !url.username().is_empty() || url.password().is_some() {
                let _ = url.set_username("xxxxx");
                let _ = url.set_password(Some("xxxxx"));
            }
            return Err(OfflineError {
                method: req.method().to_string(),
                url: url.to_string(),
            }
            .into());
        }
        Ok(self.inner.execute(req).await?)
    }
}

/// A request under construction against a [`GuardedClient`].
///
/// Wraps reqwest's builder so the only way to issue the request is
/// [`RequestBuilder::send`], which routes through the guard.
#[derive(Debug)]
pub struct RequestBuilder {
    client: GuardedClient,
    inner: reqwest::RequestBuilder,
}

impl RequestBuilder {
    /// Set a header. Takes the string forms rather than reqwest's full
    /// generic bound, which would require a direct `http` dependency
    /// this crate does not otherwise carry.
    pub fn header(self, key: &str, value: &str) -> Self {
        Self {
            inner: self.inner.header(key, value),
            ..self
        }
    }

    /// Set a bearer authorization header.
    pub fn bearer_auth<T: std::fmt::Display>(self, token: T) -> Self {
        Self {
            inner: self.inner.bearer_auth(token),
            ..self
        }
    }

    /// Set the request body.
    pub fn body<T: Into<reqwest::Body>>(self, body: T) -> Self {
        Self {
            inner: self.inner.body(body),
            ..self
        }
    }

    /// Serialize `json` as the JSON request body.
    pub fn json<T: serde::Serialize + ?Sized>(self, json: &T) -> Self {
        Self {
            inner: self.inner.json(json),
            ..self
        }
    }

    /// Append serialized `query` to the URL's query string.
    pub fn query<T: serde::Serialize + ?Sized>(self, query: &T) -> Self {
        Self {
            inner: self.inner.query(query),
            ..self
        }
    }

    /// Issue the request through the guard.
    pub async fn send(self) -> Result<reqwest::Response, NetError> {
        let req = self.inner.build()?;
        self.client.execute(req).await
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn policy_allows_loopback_while_offline() {
        let p = NetPolicy::offline();
        assert!(p.allows(&"http://127.0.0.1:8080/x".parse().unwrap()));
        assert!(p.allows(&"http://localhost/x".parse().unwrap()));
        assert!(p.allows(&"http://[::1]:9000/x".parse().unwrap()));
        assert!(!p.allows(&"https://example.com/x".parse().unwrap()));
    }

    #[test]
    fn online_policy_allows_everything() {
        let p = NetPolicy::online();
        assert!(p.allows(&"https://example.com/x".parse().unwrap()));
        assert!(p.allows(&"http://127.0.0.1/x".parse().unwrap()));
    }

    #[test]
    fn offline_error_message_names_the_flag() {
        let e = OfflineError {
            method: "GET".into(),
            url: "https://example.com/x".into(),
        };
        assert_eq!(
            e.to_string(),
            "GET https://example.com/x: network disabled by --offline"
        );
    }

    #[test]
    fn net_error_discriminates() {
        let e: NetError = OfflineError {
            method: "GET".into(),
            url: "https://example.com/x".into(),
        }
        .into();
        assert!(e.is_offline());
        assert_eq!(e.as_offline().unwrap().method, "GET");
    }
}
