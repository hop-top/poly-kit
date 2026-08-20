//! Era detection and dispatch.
//!
//! Implements ADR 0042's normative precedence rules D1–D4 and the
//! mount-option surface that selects which revisions a mount serves.
//!
//! # Modern markers
//!
//! | ID | Marker |
//! |----|--------|
//! | M1 | HTTP header `Mcp-Method` present |
//! | M2 | HTTP header `Mcp-Name` present |
//! | M3 | `params._meta` contains `io.modelcontextprotocol/protocolVersion` (key presence only) |
//! | M4 | body `method == "server/discover"` |
//!
//! # Two deliberate non-markers
//!
//! Both are subtle, and getting either wrong still passes a naive smoke
//! test while failing against real clients:
//!
//! - **Bare `params._meta` is not a marker.** 2024-11-05 clients
//!   legitimately send `_meta.progressToken` and OTel `traceparent`.
//!   Only the reserved `protocolVersion` key signals modern.
//! - **The `MCP-Protocol-Version` header is not a marker.** It predates
//!   2026-07-28 (it arrived with the 2025-06-18 transport), so clients
//!   that negotiated *down* to legacy send it on every subsequent
//!   request. Treating it as modern would serve their handshake and then
//!   brick the session. Nothing is lost: a conforming modern request
//!   always carries M1 and M3 anyway.
//!
//! # Precedence
//!
//! - **D1 — parse.** Decode the body once. Unparseable JSON is `-32700`
//!   at HTTP 400, byte-identical to the legacy response, whatever
//!   headers are present.
//! - **D2 — `initialize` is legacy, unconditionally**, even with modern
//!   markers present. A confused client gets a working legacy handshake,
//!   the most recoverable outcome; modern clients never send it.
//! - **D3 — any marker routes modern.** Incomplete or contradictory
//!   modern requests are *not* demoted to legacy; the modern handler
//!   rejects them with modern errors, which is how dual-era clients
//!   detect a modern server.
//! - **D4 — otherwise legacy.** The byte-for-byte preservation path.

use serde_json::Value;

use super::legacy::Headers;
use super::modern::{headers as modern_headers, meta_keys};
use super::wire::{codes, error_response, ErrorObject, Request, Response};
use super::{Bridge, HandlerConfig, MountOptions, SpecVersion};

/// Which handler serves a request.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Era {
    /// 2024-11-05.
    Legacy,
    /// 2026-07-28.
    Modern,
}

/// A mounted MCP surface: the transport-agnostic request handler.
///
/// Binding this to an HTTP server is the adopter's job — see
/// [`Surface::call`], which is the `tower::Service`-shaped function
/// ADR 0043 §2 specifies. The type deliberately depends on no HTTP
/// framework, so the conformance suite drives it with no socket.
#[derive(Debug)]
pub struct Surface {
    bridge: Bridge,
    cfg: HandlerConfig,
    legacy_enabled: bool,
    modern_enabled: bool,
    path: String,
}

/// A normalized inbound request.
#[derive(Debug, Clone, Default)]
pub struct HttpRequest {
    /// The HTTP method, e.g. `POST`.
    pub method: String,
    /// The request path.
    pub path: String,
    /// Headers, in wire order. Duplicates are preserved — the modern
    /// era's duplicate-header rule depends on seeing them all.
    pub headers: Vec<(String, String)>,
    /// The raw body bytes, undecoded.
    pub body: Vec<u8>,
}

impl HttpRequest {
    /// A `POST` to `path` carrying `body`.
    pub fn post(path: impl Into<String>, body: impl Into<Vec<u8>>) -> Self {
        Self {
            method: "POST".into(),
            path: path.into(),
            headers: Vec::new(),
            body: body.into(),
        }
    }

    /// Appends a header. Call repeatedly to send duplicates.
    #[must_use]
    pub fn header(mut self, name: impl Into<String>, value: impl Into<String>) -> Self {
        self.headers.push((name.into(), value.into()));
        self
    }
}

impl Surface {
    /// Mounts `bridge` with `options`.
    ///
    /// Mirrors Go's `MountMCP(bridge, router, opts...)`. Two
    /// misconfigurations are refused here rather than silently absorbed,
    /// matching Go's mount-time errors: an explicitly empty spec-version
    /// set, and a negative cache TTL.
    pub fn mount(bridge: Bridge, options: MountOptions) -> Result<Self, MountError> {
        let (legacy_enabled, modern_enabled) = match &options.spec_versions {
            // Option not supplied: both eras.
            None => (true, true),
            Some(versions) if versions.is_empty() => return Err(MountError::NoSpecVersions),
            Some(versions) => (
                versions.contains(&SpecVersion::V2024_11_05),
                versions.contains(&SpecVersion::V2026_07_28),
            ),
        };
        if options.cache_ttl_ms < 0 {
            return Err(MountError::NegativeCacheTtl);
        }
        if let Some(key) = &options.confirmation_key {
            if key.is_empty() {
                return Err(MountError::EmptyConfirmationKey);
            }
        }

        Ok(Self {
            bridge,
            path: options.path.clone(),
            cfg: HandlerConfig::from(options),
            legacy_enabled,
            modern_enabled,
        })
    }

    /// The mount path.
    #[must_use]
    pub fn path(&self) -> &str {
        &self.path
    }

    /// The bridge this surface serves.
    #[must_use]
    pub fn bridge(&self) -> &Bridge {
        &self.bridge
    }

    /// Serves one request: the `tower::Service`-shaped entry point.
    ///
    /// Pure, synchronous, and free of any HTTP framework — an adopter
    /// wraps it in axum, hyper, warp, or anything else. `GET` and
    /// `DELETE` at the mount path answer 405 when the modern era is
    /// enabled (spec SHOULD for post-session servers); the `POST` route
    /// is unaffected.
    #[must_use]
    pub fn call(&self, req: &HttpRequest) -> Response {
        if !req.method.eq_ignore_ascii_case("POST") {
            if self.modern_enabled && matches!(req.method.to_uppercase().as_str(), "GET" | "DELETE")
            {
                return method_not_allowed();
            }
            return method_not_allowed();
        }

        let headers = Headers(&req.headers);

        // D1 — parse the body once. An unparseable body is -32700 at
        // 400, byte-identical to the legacy response, whatever headers
        // are present.
        let parsed = match Request::from_slice(&req.body) {
            Ok(parsed) => parsed,
            Err(err) => {
                return error_response(
                    400,
                    None,
                    &ErrorObject {
                        code: codes::PARSE,
                        message: format!("parse error: {}", go_parse_message(&req.body, &err)),
                        data: None,
                    },
                )
            }
        };

        match self.route(&req.headers, &parsed) {
            Era::Modern => super::modern::serve(&self.cfg, &self.bridge, &parsed, &headers),
            Era::Legacy => super::legacy::serve(&self.cfg, &self.bridge, &parsed, &headers),
        }
    }

    /// Classifies a parsed request per D2–D4.
    fn route(&self, headers: &[(String, String)], req: &Request) -> Era {
        // A single-era mount has no routing decision to make.
        if !self.modern_enabled {
            return Era::Legacy;
        }
        if !self.legacy_enabled {
            // Modern only: every request takes the V1–V9 path, with no
            // special-casing of initialize anywhere.
            return Era::Modern;
        }
        detect_era(headers, req)
    }
}

/// Detects the era of a parsed request, per D2–D4.
///
/// Never fails: it only classifies. D1 (parse) is the caller's job.
#[must_use]
pub fn detect_era(headers: &[(String, String)], req: &Request) -> Era {
    // D2 — initialize is legacy, unconditionally, even when modern
    // markers are present.
    if req.method == "initialize" {
        return Era::Legacy;
    }

    // M4 — server/discover.
    if req.method == "server/discover" {
        return Era::Modern;
    }

    // M1 / M2 — header presence. Note the absence of any
    // MCP-Protocol-Version check here: that is the deliberate
    // non-marker, and adding it would brick legacy-negotiated clients.
    let present = |name: &str| {
        headers
            .iter()
            .any(|(k, v)| k.eq_ignore_ascii_case(name) && !v.is_empty())
    };
    if present(modern_headers::METHOD) || present(modern_headers::NAME) {
        return Era::Modern;
    }

    // M3 — params._meta carries the reserved protocolVersion key. Key
    // presence only; the value is never inspected at detection time.
    if req
        .meta()
        .is_some_and(|meta| meta.contains_key(meta_keys::PROTOCOL_VERSION))
    {
        return Era::Modern;
    }

    // D4 — no markers: legacy.
    Era::Legacy
}

/// Reproduces Go's `encoding/json` syntax-error text for the `-32700`
/// message, which the fixtures pin byte-exactly.
///
/// Go reports `invalid character '<c>' looking for beginning of <what>`;
/// serde's wording differs entirely, so the message is rebuilt from the
/// body rather than forwarded.
fn go_parse_message(body: &[u8], err: &serde_json::Error) -> String {
    // Locate the first byte Go would have rejected. Go scans for a
    // value and reports the first character that cannot begin one.
    let text = String::from_utf8_lossy(body);
    let mut chars = text.char_indices().skip_while(|(_, c)| c.is_whitespace());

    match chars.next() {
        Some((_, '{')) => {
            // Inside an object Go expects a quoted key next.
            let next = text[1..]
                .char_indices()
                .find(|(_, c)| !c.is_whitespace())
                .map(|(_, c)| c);
            match next {
                Some(c) if c != '"' && c != '}' => format!(
                    "invalid character '{c}' looking for beginning of object key string"
                ),
                _ => format!("unexpected end of JSON input: {err}"),
            }
        }
        Some((_, c)) if !matches!(c, '[' | '"' | '-' | 't' | 'f' | 'n') && !c.is_ascii_digit() => {
            format!("invalid character '{c}' looking for beginning of value")
        }
        _ => "unexpected end of JSON input".to_string(),
    }
}

/// The 405 response for the session-era verbs.
fn method_not_allowed() -> Response {
    error_response(
        405,
        None,
        &ErrorObject {
            code: codes::INVALID_REQUEST,
            message: "method not allowed".into(),
            data: None,
        },
    )
}

/// Why a mount was refused.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[non_exhaustive]
pub enum MountError {
    /// `spec_versions` was set to an explicitly empty list.
    NoSpecVersions,
    /// A negative cache TTL was configured.
    NegativeCacheTtl,
    /// An empty confirmation key was supplied.
    EmptyConfirmationKey,
}

impl std::fmt::Display for MountError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::NoSpecVersions => {
                f.write_str("mcp: spec_versions: at least one spec version required")
            }
            Self::NegativeCacheTtl => f.write_str("mcp: cache_hints: negative ttl"),
            Self::EmptyConfirmationKey => f.write_str("mcp: confirmation_key: empty key"),
        }
    }
}

impl std::error::Error for MountError {}

/// Serializes a [`Value`] the way the surface writes result payloads.
/// Re-exported for adopters composing their own envelopes.
#[must_use]
pub fn to_wire_bytes(value: &Value) -> Vec<u8> {
    super::wire::to_wire_bytes(value)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mcp::bridge::{CallResult, Leaf};

    fn parse(body: &str) -> Request {
        Request::from_slice(body.as_bytes()).unwrap()
    }

    fn hdrs(pairs: &[(&str, &str)]) -> Vec<(String, String)> {
        pairs
            .iter()
            .map(|(k, v)| ((*k).to_string(), (*v).to_string()))
            .collect()
    }

    #[test]
    fn initialize_is_legacy_even_with_every_modern_marker() {
        // D2 is unconditional.
        let headers = hdrs(&[
            ("Mcp-Method", "initialize"),
            ("Mcp-Name", "x"),
            ("MCP-Protocol-Version", "2026-07-28"),
        ]);
        let req = parse(
            r#"{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}"#,
        );
        assert_eq!(detect_era(&headers, &req), Era::Legacy);
    }

    #[test]
    fn bare_meta_without_the_reserved_key_stays_legacy() {
        // The progressToken non-marker: a legacy client legitimately
        // sends _meta, and must not be routed modern.
        let req = parse(
            r#"{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{"_meta":{"progressToken":"p1"}}}"#,
        );
        assert_eq!(detect_era(&[], &req), Era::Legacy);
    }

    #[test]
    fn protocol_version_header_alone_stays_legacy() {
        // The second non-marker: mid-era clients that negotiated down
        // send this header on every request.
        let headers = hdrs(&[("MCP-Protocol-Version", "2025-06-18")]);
        let req = parse(r#"{"jsonrpc":"2.0","id":8,"method":"tools/list"}"#);
        assert_eq!(detect_era(&headers, &req), Era::Legacy);
    }

    #[test]
    fn each_marker_routes_modern_on_its_own() {
        // M1
        assert_eq!(
            detect_era(
                &hdrs(&[("Mcp-Method", "tools/list")]),
                &parse(r#"{"jsonrpc":"2.0","id":1,"method":"tools/list"}"#)
            ),
            Era::Modern
        );
        // M2
        assert_eq!(
            detect_era(
                &hdrs(&[("Mcp-Name", "ping")]),
                &parse(r#"{"jsonrpc":"2.0","id":1,"method":"tools/call"}"#)
            ),
            Era::Modern
        );
        // M3 — key presence only; the value is never inspected.
        assert_eq!(
            detect_era(
                &[],
                &parse(
                    r#"{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":null}}}"#
                )
            ),
            Era::Modern
        );
        // M4
        assert_eq!(
            detect_era(&[], &parse(r#"{"jsonrpc":"2.0","id":1,"method":"server/discover"}"#)),
            Era::Modern
        );
    }

    #[test]
    fn unknown_method_without_markers_stays_legacy() {
        let req = parse(r#"{"jsonrpc":"2.0","id":6,"method":"nope"}"#);
        assert_eq!(detect_era(&[], &req), Era::Legacy);
    }

    fn surface(options: MountOptions) -> Surface {
        let bridge = Bridge::new().leaf(Leaf::new(&["ping"], "Ping the server", |_| {
            Ok(CallResult::ok("pong\n"))
        }));
        Surface::mount(bridge, options).unwrap()
    }

    #[test]
    fn empty_spec_version_set_is_a_mount_error() {
        let options = MountOptions {
            spec_versions: Some(Vec::new()),
            ..MountOptions::default()
        };
        assert_eq!(
            Surface::mount(Bridge::new(), options).unwrap_err(),
            MountError::NoSpecVersions
        );
    }

    #[test]
    fn negative_cache_ttl_is_a_mount_error() {
        let options = MountOptions {
            cache_ttl_ms: -1,
            ..MountOptions::default()
        };
        assert_eq!(
            Surface::mount(Bridge::new(), options).unwrap_err(),
            MountError::NegativeCacheTtl
        );
    }

    #[test]
    fn empty_confirmation_key_is_a_mount_error() {
        let options = MountOptions {
            confirmation_key: Some(Vec::new()),
            ..MountOptions::default()
        };
        assert_eq!(
            Surface::mount(Bridge::new(), options).unwrap_err(),
            MountError::EmptyConfirmationKey
        );
    }

    #[test]
    fn legacy_only_mount_ignores_modern_markers() {
        let surface = surface(MountOptions {
            spec_versions: Some(vec![SpecVersion::V2024_11_05]),
            ..MountOptions::default()
        });
        // Markers present, but there is no modern handler mounted, so
        // the request takes today's exact code path.
        let req = HttpRequest::post("/mcp", r#"{"jsonrpc":"2.0","id":1,"method":"tools/list"}"#)
            .header("Mcp-Method", "tools/list");
        let resp = surface.call(&req);
        assert_eq!(resp.status, 200);
        assert!(!resp.body_str().contains("resultType"));
    }

    #[test]
    fn modern_only_mount_does_not_demote_initialize() {
        let surface = surface(MountOptions {
            spec_versions: Some(vec![SpecVersion::V2026_07_28]),
            ..MountOptions::default()
        });
        let req = HttpRequest::post("/mcp", r#"{"jsonrpc":"2.0","id":1,"method":"initialize"}"#);
        let resp = surface.call(&req);
        // Fails V3 rather than getting a legacy handshake.
        assert_eq!(resp.status, 400);
        assert!(resp
            .body_str()
            .contains("supported protocol versions: 2026-07-28"));
    }

    #[test]
    fn get_and_delete_answer_405() {
        let surface = surface(MountOptions::default());
        for method in ["GET", "DELETE"] {
            let req = HttpRequest {
                method: method.into(),
                path: "/mcp".into(),
                headers: Vec::new(),
                body: Vec::new(),
            };
            let resp = surface.call(&req);
            assert_eq!(resp.status, 405);
            assert!(resp.body_str().contains("method not allowed"));
        }
    }

    #[test]
    fn unparseable_body_is_32700_at_400_regardless_of_headers() {
        let surface = surface(MountOptions::default());
        // Even with every modern marker set, D1 wins.
        let req = HttpRequest::post("/mcp", "{not json")
            .header("Mcp-Method", "tools/list")
            .header("MCP-Protocol-Version", "2026-07-28");
        let resp = surface.call(&req);
        assert_eq!(resp.status, 400);
        assert_eq!(
            resp.body_str(),
            "{\"jsonrpc\":\"2.0\",\"error\":{\"code\":-32700,\"message\":\
             \"parse error: invalid character 'n' looking for beginning of \
             object key string\"}}\n"
        );
    }
}
