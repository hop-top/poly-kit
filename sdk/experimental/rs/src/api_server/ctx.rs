use std::net::IpAddr;

use axum::extract::{ConnectInfo, Request};
use std::net::SocketAddr;

/// The caller's address the stack acts on: the peer, or the client a
/// trusted proxy forwarded. In request extensions.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ClientIp(pub IpAddr);

/// The peer socket address when a trusted proxy's `Forwarded` data
/// replaced the client address. In request extensions.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct PeerAddr(pub SocketAddr);

/// The scheme a trusted proxy reported (`https`, `wss`...). In request
/// extensions.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ForwardedProto(pub String);

/// Set once a verifier accepted the credential: a bare Authorization
/// header is not authentication, only the verdict is. In request
/// extensions, with the [`Claims`](crate::authn::Claims).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Authenticated;

/// Whether the request reached the listener over TLS, as a trusted
/// proxy reported it.
pub fn is_https(req: &Request) -> bool {
    req.extensions()
        .get::<ForwardedProto>()
        .is_some_and(|p| p.0 == "https" || p.0 == "wss")
}

/// The peer the connection came from, when the server recorded it.
pub(crate) fn peer(req: &Request) -> Option<SocketAddr> {
    req.extensions()
        .get::<ConnectInfo<SocketAddr>>()
        .map(|c| c.0)
}

/// The request's `Host`: the header, else the URI authority.
pub(crate) fn host_of(req: &Request) -> String {
    req.headers()
        .get(axum::http::header::HOST)
        .and_then(|h| h.to_str().ok())
        .map(str::to_string)
        .or_else(|| req.uri().authority().map(|a| a.as_str().to_string()))
        .unwrap_or_default()
}
