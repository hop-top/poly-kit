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
//!   connects, the address is disconnected, or the adapter closes.
//! - Close sends 1001 to every peer and waits for the readers.
//!
//! # Differences from Go
//!
//! - The I/O runs on a dedicated runtime thread (see [`TokioSpawn`] for
//!   the executor seam); blocking methods have `_async` twins.
//! - Accepting is [`NetworkAdapter::listen`] or
//!   [`NetworkAdapter::accept_upgraded`], in place of an `http.Handler`.
//! - Not yet ported: streams (resume, subscriptions, outbox), the
//!   `kit.runtime.link.*` events, CloudEvents outbound frames and the
//!   error callback. Inbound frames on those topics are dropped, as Go
//!   does.

mod accept;
mod adapter;
mod auth;
mod conn;
mod ctx;
mod error;
mod filter;
mod reconnect;
mod relay;
mod runtime;
mod session;
mod sync;
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
