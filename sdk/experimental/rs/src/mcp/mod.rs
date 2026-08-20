//! Dual-spec MCP surface. Gated behind the `mcp` feature.
//!
//! One mount serves **both** MCP revisions — 2024-11-05 (the
//! `initialize` handshake) and 2026-07-28 (the stateless per-request
//! envelope) — with the era detected per request. Ports Go's
//! `go/transport/cmdsurface` MCP surface; see ADR 0043 for the polyglot
//! design and ADR 0042 for the normative detection and validation rules.
//!
//! # Parity contract
//!
//! Wire behavior is pinned byte-for-byte by
//! `sdk/tests/cross-lang/fixtures/mcp-wire.json`, generated from the Go
//! surface and executed here by `tests/mcp_wire_conformance.rs`. Where
//! this documentation and the fixtures disagree, **the fixtures win**.
//!
//! # Hosting
//!
//! [`Surface::call`] is a plain request → response function, the
//! `tower::Service` shape ADR 0043 §2 fixes. This crate binds to no HTTP
//! server: adopters wire it to axum, hyper, warp, or anything else, and
//! the conformance suite drives it with no socket at all.
//!
//! # Module split
//!
//! [`legacy`] and [`modern`] are separate modules on purpose, with
//! [`dispatch`] choosing between them. The split is load-bearing rather
//! than cosmetic: it keeps the "2024-11-05 is preserved byte-for-byte,
//! additive only" invariant structurally checkable, so a reviewer can
//! confirm a modern-era change never reached into the legacy path.
//!
//! # Safety
//!
//! Exposure is gated by [`safety::Policy`], ported from Go's
//! `cmdsurface/safety.go`. The default blocks **every** remote
//! destructive invocation, and a blocked call renders as an `isError`
//! result at HTTP 200 — not a transport error.
//!
//! # Example
//!
//! ```
//! use hop_top_kit::mcp::{Bridge, CallResult, HttpRequest, Leaf, MountOptions, Surface};
//!
//! let bridge = Bridge::new().leaf(Leaf::new(&["ping"], "Ping the server", |_| {
//!     Ok(CallResult::ok("pong\n"))
//! }));
//! let surface = Surface::mount(bridge, MountOptions::default()).unwrap();
//!
//! let request = HttpRequest::post(
//!     "/mcp",
//!     r#"{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}"#,
//! );
//! let response = surface.call(&request);
//!
//! assert_eq!(response.status, 200);
//! assert!(response.body_str().contains("pong"));
//! ```

pub mod bridge;
pub mod dispatch;
pub mod legacy;
pub mod modern;
pub mod safety;
pub mod tasks;
pub mod wire;

pub use bridge::{Bridge, CallResult, FlagSchema, InvokeError, Leaf};
pub use modern::{request_meta, RequestMeta};
pub use dispatch::{detect_era, Era, HttpRequest, MountError, Surface};
pub use safety::{Policy, SafetyClass, Surface as SurfaceKind};
pub use wire::{Request, Response};

/// An MCP protocol revision this surface can serve.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
#[non_exhaustive]
pub enum SpecVersion {
    /// The handshake era: `initialize`, `tools/list`, `tools/call`.
    V2024_11_05,
    /// The stateless era: `server/discover`, per-request `_meta`.
    V2026_07_28,
}

impl SpecVersion {
    /// The wire string, e.g. `"2026-07-28"`.
    #[must_use]
    pub fn as_str(self) -> &'static str {
        match self {
            Self::V2024_11_05 => legacy::PROTOCOL_VERSION,
            Self::V2026_07_28 => modern::PROTOCOL_VERSION,
        }
    }
}

/// The `cacheScope` attached to modern cacheable list results.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
#[non_exhaustive]
pub enum CacheScope {
    /// Safe default: the result may depend on the caller.
    #[default]
    Private,
    /// The tool list is caller-independent.
    Public,
}

impl CacheScope {
    /// The wire string.
    #[must_use]
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Private => "private",
            Self::Public => "public",
        }
    }
}

/// Mount-time configuration.
///
/// The option *set* is normative across all four ports (ADR 0043 §3);
/// the spelling is idiomatic per language. Defaults match Go exactly:
/// both eras enabled, path `/mcp`, empty origin allowlist, and cache
/// hints `ttl_ms = 0` / `cache_scope = private`.
#[derive(Debug, Clone)]
pub struct MountOptions {
    /// Mount path. Defaults to `/mcp`.
    pub path: String,
    /// `serverInfo.name`. Defaults to `cmdsurface`.
    pub server_name: String,
    /// `serverInfo.version`. Defaults to `0.0.0`.
    pub server_version: String,
    /// Which eras to serve. `None` means both — an explicitly empty
    /// list is a mount error, matching Go's refusal.
    pub spec_versions: Option<Vec<SpecVersion>>,
    /// `ttlMs` on cacheable list results. Negative is a mount error.
    pub cache_ttl_ms: i64,
    /// `cacheScope` on cacheable list results.
    pub cache_scope: CacheScope,
    /// Exact-match Origin allowlist for the modern path. Empty disables
    /// the check (deployment-proxy responsibility).
    pub origin_allowlist: Vec<String>,
    /// HMAC key enabling the MRTR confirmation flow. `None` keeps the
    /// `X-Confirm-Token` header gate; `Some(empty)` is a mount error.
    ///
    /// Multi-instance deployments must share one key, so a retry landing
    /// on any instance can verify state minted by another. There is
    /// deliberately no generated-at-mount default, which would silently
    /// break exactly that guarantee.
    pub confirmation_key: Option<Vec<u8>>,
    /// Whether to declare the tasks extension on `server/discover`.
    pub tasks_enabled: bool,
}

impl Default for MountOptions {
    fn default() -> Self {
        Self {
            path: "/mcp".into(),
            server_name: "cmdsurface".into(),
            server_version: "0.0.0".into(),
            spec_versions: None,
            cache_ttl_ms: 0,
            cache_scope: CacheScope::Private,
            origin_allowlist: Vec::new(),
            confirmation_key: None,
            tasks_enabled: false,
        }
    }
}

/// The immutable per-mount configuration the handlers read.
#[derive(Debug, Clone)]
pub(crate) struct HandlerConfig {
    pub(crate) server_name: String,
    pub(crate) server_version: String,
    pub(crate) cache_ttl_ms: i64,
    pub(crate) cache_scope: CacheScope,
    pub(crate) origin_allowlist: Vec<String>,
    pub(crate) confirmation_key: Option<Vec<u8>>,
    pub(crate) tasks_enabled: bool,
}

impl Default for HandlerConfig {
    fn default() -> Self {
        Self::from(MountOptions::default())
    }
}

impl From<MountOptions> for HandlerConfig {
    fn from(options: MountOptions) -> Self {
        Self {
            server_name: options.server_name,
            server_version: options.server_version,
            cache_ttl_ms: options.cache_ttl_ms,
            cache_scope: options.cache_scope,
            origin_allowlist: options.origin_allowlist,
            confirmation_key: options.confirmation_key,
            tasks_enabled: options.tasks_enabled,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn defaults_match_the_go_mount() {
        let options = MountOptions::default();
        assert_eq!(options.path, "/mcp");
        assert_eq!(options.server_name, "cmdsurface");
        assert_eq!(options.server_version, "0.0.0");
        assert_eq!(options.cache_ttl_ms, 0);
        assert_eq!(options.cache_scope, CacheScope::Private);
        assert!(options.origin_allowlist.is_empty());
        assert!(
            options.spec_versions.is_none(),
            "absent means both eras enabled"
        );
        assert!(options.confirmation_key.is_none());
    }

    #[test]
    fn spec_version_wire_strings() {
        assert_eq!(SpecVersion::V2024_11_05.as_str(), "2024-11-05");
        assert_eq!(SpecVersion::V2026_07_28.as_str(), "2026-07-28");
    }

    #[test]
    fn cache_scope_wire_strings() {
        assert_eq!(CacheScope::Private.as_str(), "private");
        assert_eq!(CacheScope::Public.as_str(), "public");
        assert_eq!(CacheScope::default(), CacheScope::Private);
    }
}
