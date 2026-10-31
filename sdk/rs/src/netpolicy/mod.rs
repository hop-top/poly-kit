//! Network policy marker and the client that enforces it.
//!
//! The `--offline` global (cli-parity-guide, "Global Flags") promises
//! that network access is disabled. Carrying that promise as a plain
//! flag value cannot keep it: it is advisory, so any caller that forgets
//! to consult it still reaches the wire. `GuardedClient` closes that
//! gap by refusing the request at the point where it is sent, beneath
//! every caller, where none can route around it.
//!
//! # Why this shape
//!
//! The Go side installs an `http.RoundTripper` over the process-global
//! `http.DefaultTransport`, so a bare `&http.Client{}` inherits the
//! policy without a per-site change. Rust has no such process-global:
//! `reqwest::Client::new()` builds an independent stack every time, and
//! nothing reqwest 0.13 exposes lets a policy see a request before it is
//! sent. Its `connector_layer` hook receives an opaque `Unnameable` (no
//! destination to inspect) and fires only on new connections, so pooled
//! reuse would slip past; a custom `dns_resolver` never sees IP-literal
//! URLs at all and cannot surface a typed error to the caller.
//!
//! So the chokepoint here is construction rather than transport: this
//! module owns the port's `reqwest::Client` construction path for user
//! traffic (the telemetry sink's own client is the one, deliberate,
//! exception: see Scope), and
//! hands back a `GuardedClient` that does not expose the inner
//! `reqwest::Client`. Every request necessarily passes through
//! `GuardedClient::execute`, which is where the policy is applied.
//!
//! # Marker
//!
//! Rust has no ambient request-scoped context, so the marker is an
//! explicit [`NetPolicy`] value threaded at client construction rather
//! than a context value. [`NetPolicy::default`] is online, so a port
//! that has not wired the flag behaves exactly as it did before.
//!
//! # Allowlist
//!
//! `--offline` is all or nothing. [`NetPolicy::with_allow_hosts`] sits
//! between: dials and requests are permitted only to the configured
//! hosts (config key `netpolicy.allow_hosts`; exact entries and
//! `*.suffix`), so a tool can promise it talks to Graph and its login
//! endpoint and nowhere else. A refusal is typed ([`HostNotAllowedError`])
//! and, when a sink is set with [`NetPolicy::with_audit`], audited.
//! Offline wins when both apply; loopback and unix sockets stay exempt
//! under either.
//!
//! Loopback is deliberately exempt. `--offline` means "do not talk to
//! the network", not "do not talk to myself": a local peer, a dev
//! backend on 127.0.0.1 and unix sockets stay reachable so offline
//! workflows remain usable.
//!
//! # Features
//!
//! The decision lives in the `netpolicy` feature, which depends only
//! on `url` (host normalization) and builds on wasm32: [`NetPolicy`],
//! [`is_loopback`], [`NetPolicy::check_dial`], [`OfflineError`],
//! [`HostNotAllowedError`]. The reqwest client
//! (`GuardedClient`, `NetError`, `RequestBuilder`, `NetPolicy::allows`)
//! sits under `api`, which enables `netpolicy`.
//!
//! # Scope
//!
//! `GuardedClient` covers HTTP and HTTPS through reqwest — every
//! network client in this port today except the telemetry HTTPS sink,
//! which is deliberately unguarded: telemetry is logging-class egress,
//! not traffic `--offline` stops. It does NOT cover code that opens
//! a socket directly: raw `TcpStream`, websockets, SQL drivers, gRPC or
//! raw TLS. Those must call [`NetPolicy::check_dial`] before dialing,
//! the analogue of routing a Go dialer through `GuardDial`. It also
//! cannot reach a `reqwest::Client` a consumer builds on its own,
//! outside this module — that is the price of Rust having no default
//! transport to install over.

mod core;

pub use self::core::{
    is_loopback, validate_allow_hosts, HostNotAllowedError, InvalidAllowHost, NetPolicy,
    OfflineError, PolicyError, Refusal, RefusalReason, HOST_NOT_ALLOWED_MESSAGE, OFFLINE_MESSAGE,
};

#[cfg(feature = "api")]
mod reqwest;

#[cfg(feature = "api")]
pub(crate) use self::reqwest::destination;
#[cfg(feature = "api")]
pub use self::reqwest::{GuardedClient, NetError, RedirectError, Redirects, RequestBuilder};
