//! The unix-socket transport: a port of `go/transport/socket`.
//!
//! Serves a handler map as newline-delimited JSON over a Unix domain
//! socket: one request object per line, one response per line, answered
//! in order. It is the local-only transport, where the socket file's
//! `0600` permission is the access control; [`SocketService`] is a
//! [`serve::Service`](crate::serve::Service), so it starts, reports
//! ready and drains under the same supervisor as any other service.
//!
//! Wire types ([`Request`], [`Response`], [`WireError`]) and the
//! `CODE_*` symbols are Go's. Commands are an explicit [`Handlers`]
//! map; projecting a command tree onto it waits for the kit-rs CLI
//! root.
//!
//! See the README beside this file for the contract.

mod bind;
mod handler;
mod ledger;
mod peer;
mod transport;
mod wire;

pub use handler::{CommandMeta, Handler, HandlerFuture, Handlers, RouteError};
pub use ledger::DEFAULT_TTL;
pub use peer::{
    Authenticator, Connection, Identity, PeerAuthConfig, PeerAuthenticator, PeerCred, Refusal,
    EXTRA_PEER_GID, EXTRA_PEER_PID, EXTRA_PEER_UID,
};
pub use transport::SocketService;
pub use wire::*;
