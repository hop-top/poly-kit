#[cfg(feature = "cli")]
pub mod cli;
#[cfg(feature = "output")]
pub mod output;

// Terminal UI primitives (a port of go/console/tui and go/console/progress):
// std only, plain ANSI, no raw mode.
#[cfg(feature = "tui")]
pub mod tui;

#[cfg(feature = "uri")]
pub mod uri;

#[cfg(feature = "api")]
pub mod api;

// Network policy marker + the guarded client every user-traffic reqwest
// request in this crate is issued through. The marker, loopback check
// and dial check are dependency-free under `netpolicy`; the reqwest
// client inside is gated on `api`, which enables `netpolicy`.
// `telemetry` enables `api`, but its HTTPS sink builds its own client
// and is deliberately unguarded: telemetry is logging-class egress, not
// traffic `--offline` stops.
#[cfg(feature = "netpolicy")]
pub mod netpolicy;

// Bearer-token and API-key authentication (a port of go/transport/authn).
// Pure and wasm-safe; the API-key store needs `authn-apikeys`.
#[cfg(feature = "authn")]
pub mod authn;

// The REST server, a port of go/transport/api and the Go api service's
// middleware plane. Native only.
#[cfg(feature = "api-server")]
pub mod api_server;

#[cfg(feature = "blob")]
pub mod blob;

#[cfg(feature = "id")]
pub mod id;

#[cfg(feature = "bus")]
pub mod bus;

#[cfg(feature = "domain")]
pub mod domain;

#[cfg(feature = "breaker")]
pub mod breaker;

#[cfg(feature = "sync")]
pub mod sync;

#[cfg(feature = "config")]
pub mod config;

#[cfg(feature = "consent")]
pub mod consent;

// The FS, HTTP, bus and subprocess seams with dryrun and testfake
// implementations (a port of go/runtime/sideeffect). The real ones are
// native only.
#[cfg(feature = "sideeffect")]
pub mod sideeffect;

// Secret and PII redaction (a port of go/core/redact). No I/O.
#[cfg(feature = "redact")]
pub mod redact;

// Declarative CEL guard engine on the veto-able pre-events (a port of
// go/runtime/policy).
#[cfg(feature = "policy")]
pub mod policy;
// Local-first cryptographic identity (a port of go/core/identity):
// Ed25519 keypair, EdDSA JWT, key derivation and secretbox. The core is
// wasm-safe; OS-RNG generation, the system clock and the store are
// native only.
#[cfg(feature = "identity")]
pub mod identity;

// Base directories for config, consent, telemetry and upgrade files (a
// port of go/core/xdg's home, config-home, data-home and state-home
// resolution). Filesystem-only: off wasm32.
#[cfg(all(
    any(
        feature = "config",
        feature = "telemetry",
        feature = "upgrade",
        feature = "identity"
    ),
    not(target_arch = "wasm32")
))]
pub mod xdg;

#[cfg(feature = "upgrade")]
pub mod upgrade;

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

#[cfg(feature = "llm")]
pub mod llm;

#[cfg(feature = "mcp")]
pub mod mcp;

#[cfg(feature = "notify")]
pub mod notify;

#[cfg(feature = "serve")]
pub mod serve;
#[cfg(all(feature = "socket", unix))]
pub mod socket;

// Retry/backoff policy. Pure std: no feature gate, wasm32-safe.
pub mod util;
