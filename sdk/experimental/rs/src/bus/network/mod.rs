//! The bus network adapter: Go's `NetworkAdapter` over a native
//! WebSocket. Gated behind the `bus-network` feature.
//!
//! A [`NetworkAdapter`] bridges a bus to remote peers. Events published
//! locally go to every connected peer the [`TopicFilter`] lets through;
//! events read from a peer are published locally, marked so they are not
//! forwarded again. Every frame carries the origin id of the adapter that
//! first published it, which stops loops; with relay on, a hub forwards
//! inbound events to every other peer, so a star of peers sees each
//! event once.
//!
//! [`DynBus::builder().with_network(addrs)`](crate::bus::DynBusBuilder::with_network)
//! attaches one and dials each address, as Go's `WithNetwork` does.
//!
//! # Wire
//!
//! JSON text frames, the same as Go's links without streams: an optional
//! auth frame `{"auth":"<token>"}` answered by `{"type":"auth_ok"}`,
//! then kit event frames `{topic, source, timestamp, payload, origin,
//! workspace_id?, id?}`. Inbound CloudEvents frames are read too. A Go
//! peer that speaks streams falls back to this, so Go and Rust peers mix
//! in either role. Pinned by `contracts/bus-v1/streams/wire/plain_ack.jsonl`.
//!
//! # Behaviour, as in Go
//!
//! - Connect checks the context's [`NetPolicy`](crate::netpolicy::NetPolicy)
//!   before dialing; an offline policy is remembered for reconnects.
//! - Auth: the token goes out first and `auth_ok` must come back within
//!   10 s; refusals close with 1008, internal failures with 1011.
//! - Writes time out after 5 s; three failures in a row evict the link,
//!   and an outbound one reconnects.
//! - A dropped outbound link reconnects with jittered backoff (100 ms to
//!   30 s), each attempt bounded by 10 s, one loop per address, until it
//!   connects, the address is disconnected, or the adapter closes. The
//!   redial waits the backoff first, and links that drop within the
//!   stability window (the maximum backoff) keep growing it, so a peer
//!   that closes every link at once is not hammered.
//! - Close sends 1001 to every peer and waits for the readers.
//! - The adapter publishes its own events on the local bus, never
//!   forwarded: `kit.runtime.link.connected` (`peer`, `stream` empty,
//!   `resume` `unsupported`, `format` `kit`) when an outbound link comes
//!   up, `kit.runtime.link.disconnected` (`peer`, `final`, `code` when
//!   known: `auth`, `timeout`) when one goes down or a handshake is
//!   refused (once per change of code), and
//!   `kit.runtime.stream.invalidated` (`reason` `unsupported`) when a link
//!   comes back: events may have gone by while it was down.
//! - Errors no caller can receive (a reconnect's refused handshake) go to
//!   [`NetworkOptions::error_func`].
//! - Relay never sends an event back on the link it arrived on; a frame
//!   without an origin takes the link's, else this adapter's; a second
//!   inbound link under a linked address is refused (1000 `duplicate`).
//! - The acceptor applies coder/websocket's default checks, the Origin
//!   check included ([`check_upgrade`]).
//!
//! # Differences from Go
//!
//! - The I/O runs on a dedicated runtime thread (see [`TokioSpawn`] for
//!   the executor seam); blocking methods have `_async` twins.
//! - Accepting is [`NetworkAdapter::listen`], [`NetworkAdapter::accept_upgraded`]
//!   after [`check_upgrade`], or the axum handler (`bus-network-axum`),
//!   in place of an `http.Handler`.
//! - `wss://` trusts the platform's roots by default (Go: the default
//!   transport); [`NetworkOptions::tls_config`] takes a rustls config,
//!   which Android needs. Feature `bus-network-webpki-roots` adds
//!   Mozilla's roots to the defaults.
//! - Not yet ported: streams (resume, subscriptions, outbox, positions),
//!   CloudEvents outbound frames and binary CloudEvents data inbound.
//!   Inbound frames on the adapter's own topics are dropped, as Go does.

mod accept;
mod adapter;
mod auth;
#[cfg(feature = "bus-network-axum")]
mod axum;
mod conn;
mod ctx;
mod error;
mod filter;
mod options;
mod reconnect;
mod relay;
mod runtime;
mod session;
mod sync;
mod upgrade;
mod wire;

#[cfg(test)]
mod tests;

pub use adapter::{NetworkAdapter, NetworkOptions};
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
pub use auth::auth_from_env;
pub use auth::{Authenticator, OpenAuth, StaticTokenAuth};
pub use error::{AuthError, NetworkError};
pub use filter::TopicFilter;
pub use runtime::TokioSpawn;
pub use upgrade::{check_upgrade, Rejection, Upgrade};
