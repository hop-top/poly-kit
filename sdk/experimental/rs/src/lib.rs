#[cfg(feature = "cli")]
pub mod cli;
#[cfg(feature = "output")]
pub mod output;
pub mod tui;

#[cfg(feature = "uri")]
pub mod uri;

#[cfg(feature = "api")]
pub mod api;

// Network policy marker + the guarded client every reqwest request in
// this crate is issued through. Gated on `api` because that is the
// feature that brings reqwest in; `telemetry` enables `api` so its
// HTTPS sink is covered too.
#[cfg(feature = "api")]
pub mod netpolicy;

#[cfg(feature = "blob")]
pub mod blob;

#[cfg(feature = "id")]
pub mod id;

#[cfg(feature = "bus")]
pub mod bus;

#[cfg(feature = "sqldb")]
pub mod sqldb;

#[cfg(feature = "kv")]
pub mod kv;

#[cfg(feature = "httpcache")]
pub mod httpcache;

#[cfg(feature = "sqlstore")]
pub mod sqlstore;

#[cfg(feature = "telemetry")]
pub mod telemetry;

#[cfg(feature = "timeutil")]
pub mod timeutil;

#[cfg(feature = "mcp")]
pub mod mcp;
