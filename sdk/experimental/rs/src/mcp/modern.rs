//! The 2026-07-28 handler: the stateless request core.
//!
//! Implements ADR 0042's normative validation order V1–V9. The first
//! failure responds and stops. HTTP status is 400/404 only where the
//! spec mandates it; application-level JSON-RPC errors ride HTTP 200,
//! matching the legacy convention.
//!
//! | # | Check | Failure |
//! |---|-------|---------|
//! | V1 | `jsonrpc` absent or `"2.0"` | `-32600` @ 400 |
//! | V2 | `id` present and a string/integer | `-32600` @ 400 (absent → 202) |
//! | V3 | `params._meta` carries the required reserved keys | `-32602` @ 400 |
//! | V4 | `MCP-Protocol-Version` header matches `_meta` | `-32020` @ 400 |
//! | V5 | requested version is served | `-32022` @ 400 |
//! | V6 | `Mcp-Method` header matches body `method` | `-32020` @ 400 |
//! | V7 | `tools/call`: `Mcp-Name` matches `params.name` | `-32020` @ 400 |
//! | V8 | method is one of the three served | `-32601` @ 404 |
//! | V9 | per-method params | `-32602` @ 200 |
//!
//! Statelessness is the revision's core contract: every request carries
//! its protocol version, client identity, and capabilities in
//! `params._meta`, so any instance can serve any request. The handler
//! holds only immutable mount configuration.

use serde_json::{Map, Value};

use super::bridge::{Bridge, InvokeError};
use super::legacy::{error_result_block, render_call_result, Headers};
use super::safety::Surface;
use super::tasks;
use super::wire::{codes, error_response, result_response, ErrorObject, Request, Response};
use super::HandlerConfig;

/// The protocol revision this handler speaks.
///
/// ADR 0043 §1 fixes `rmcp` as the protocol layer, so this string is not
/// an independent spelling: `protocol_version_matches_sdk` below asserts
/// it equals `rmcp::model::ProtocolVersion::V_2026_07_28`, which cannot
/// be used directly in const position (it wraps a `Cow`).
pub const PROTOCOL_VERSION: &str = "2026-07-28";

/// Reserved `_meta` keys, as fixed by the spec and mirrored in
/// `rmcp::model`.
pub mod meta_keys {
    /// Per-request protocol version. Its presence is marker M3.
    pub const PROTOCOL_VERSION: &str = "io.modelcontextprotocol/protocolVersion";
    /// Optional client identity, used for audit metadata only.
    pub const CLIENT_INFO: &str = "io.modelcontextprotocol/clientInfo";
    /// Required client capability declaration.
    pub const CLIENT_CAPABILITIES: &str = "io.modelcontextprotocol/clientCapabilities";
    /// Server identity, stamped on every modern result's `_meta`.
    pub const SERVER_INFO: &str = "io.modelcontextprotocol/serverInfo";
}

/// Header names the modern era validates.
pub mod headers {
    /// Must equal the `_meta` protocol version (V4).
    pub const PROTOCOL_VERSION: &str = "MCP-Protocol-Version";
    /// Must equal the body `method` (V6). Presence is marker M1.
    pub const METHOD: &str = "Mcp-Method";
    /// Must equal `params.name` on `tools/call` (V7). Presence is M2.
    pub const NAME: &str = "Mcp-Name";
}

/// `resultType` values. Everything is `complete` — tool-execution
/// errors included, per spec — except MRTR interim results.
pub const RESULT_TYPE_COMPLETE: &str = "complete";
/// The MRTR interim `resultType`.
pub const RESULT_TYPE_INPUT_REQUIRED: &str = "input_required";

/// One validation-chain failure.
struct CheckError {
    code: i64,
    msg: String,
    status: u16,
    data: Option<Value>,
}

/// The decoded reserved `_meta` keys (V3).
#[derive(Debug, Clone)]
pub struct RequestMeta {
    /// The protocol version when it decoded as a JSON string.
    version: Option<String>,
    /// The raw value whatever its type; V5 echoes it as `requested`.
    version_raw: Value,
    /// Client identity, when `clientInfo` was present and an object.
    pub(super) client_name: Option<String>,
    /// Client version, when `clientInfo` was present and an object.
    pub(super) client_version: Option<String>,
    /// The declared client capabilities.
    pub(super) capabilities: Value,
}

impl RequestMeta {
    /// The audit metadata a sink records for this invocation.
    ///
    /// Mirrors Go's `Meta.Extra` bag: the spec version always, plus the
    /// client identity when the request carried `clientInfo`. Adopters
    /// forward this to their audit sink; the surface itself has no sink
    /// abstraction to push it into.
    #[must_use]
    pub fn audit_extra(&self) -> Vec<(&'static str, String)> {
        let mut extra = vec![("mcp_spec_version", PROTOCOL_VERSION.to_owned())];
        if let Some(name) = &self.client_name {
            extra.push(("mcp_client_name", name.clone()));
        }
        if let Some(version) = &self.client_version {
            extra.push(("mcp_client_version", version.clone()));
        }
        extra
    }
}

/// Decodes the reserved `_meta` keys of a modern request.
///
/// Exposed so adopters can read the audit metadata (see
/// [`RequestMeta::audit_extra`]) for a request the surface accepted.
/// Returns `None` when the request does not carry a conforming modern
/// envelope.
#[must_use]
pub fn request_meta(req: &Request) -> Option<RequestMeta> {
    parse_meta(req).ok()
}

/// Serves one already-parsed request on the modern era.
pub(super) fn serve(
    cfg: &HandlerConfig,
    bridge: &Bridge,
    req: &Request,
    headers: &Headers,
) -> Response {
    // Origin allowlist (opt-in) runs before any protocol validation.
    if !origin_allowed(cfg, headers) {
        return write_error(
            req,
            &CheckError {
                code: codes::INVALID_REQUEST,
                msg: "origin not allowed".into(),
                status: 403,
                data: None,
            },
        );
    }

    // V1 — jsonrpc member.
    if !req.jsonrpc_ok() {
        return write_error(
            req,
            &CheckError {
                code: codes::INVALID_REQUEST,
                msg: "invalid jsonrpc version".into(),
                status: 400,
                data: None,
            },
        );
    }

    // V2 — id shape. Absent means notification: HTTP 202, empty body,
    // discarded without processing.
    let Some(id) = req.id.as_ref() else {
        return Response::empty(202);
    };
    if !valid_request_id(id) {
        let raw = String::from_utf8(super::wire::to_wire_bytes(id)).unwrap_or_default();
        return write_error(
            req,
            &CheckError {
                code: codes::INVALID_REQUEST,
                msg: format!("invalid request id: must be a string or integer, got {raw}"),
                status: 400,
                data: None,
            },
        );
    }

    // V3 — required reserved _meta keys.
    let meta = match parse_meta(req) {
        Ok(meta) => meta,
        Err(err) => return write_error(req, &err),
    };

    // V4 — MCP-Protocol-Version header agreement.
    if let Err(err) = validate_version_header(headers, &meta) {
        return write_error(req, &err);
    }

    // V5 — requested version served.
    if meta.version.as_deref() != Some(PROTOCOL_VERSION) {
        let requested = meta.version_raw.clone();
        let mut data = Map::new();
        data.insert(
            "supported".into(),
            Value::Array(vec![Value::String(PROTOCOL_VERSION.into())]),
        );
        data.insert("requested".into(), requested);
        return write_error(
            req,
            &CheckError {
                code: codes::UNSUPPORTED_VERSION,
                msg: format!(
                    "unsupported protocol version: {}",
                    meta.version.clone().unwrap_or_default()
                ),
                status: 400,
                data: Some(Value::Object(data)),
            },
        );
    }

    // V6 — Mcp-Method header agreement.
    if let Err(err) = validate_method_header(headers, req) {
        return write_error(req, &err);
    }

    // V8 — method routing. V7 and V9 run inside the method handlers.
    match req.method.as_str() {
        "server/discover" => handle_discover(cfg, req),
        "tools/list" => handle_tools_list(cfg, bridge, req),
        "tools/call" => handle_tools_call(cfg, bridge, req, headers, &meta),
        other => write_error(
            req,
            &CheckError {
                code: codes::METHOD_NOT_FOUND,
                msg: format!("method not found: {other}"),
                // 404, unlike legacy's 200 for the same condition.
                status: 404,
                data: None,
            },
        ),
    }
}

/// Reduces a possibly-duplicated header to one value for comparison.
///
/// A header sent once, or sent repeatedly with byte-identical values
/// (benign proxy duplication), resolves to that value. Repeated with
/// *differing* values it is a validation failure in its own right —
/// these headers exist so gateways and the server agree on one routing
/// signal, and conflicting duplicates are exactly the
/// multiple-sources-of-truth hazard the agreement check closes. `Err`
/// means "never singular", so the caller rejects without comparing.
fn single_header<'a>(headers: &'a Headers, name: &str) -> Result<Option<&'a str>, ()> {
    let values = headers.all(name);
    match values.len() {
        0 => Ok(None),
        1 => Ok(Some(values[0])),
        _ => {
            if values[1..].iter().all(|v| *v == values[0]) {
                Ok(Some(values[0]))
            } else {
                Err(())
            }
        }
    }
}

/// V2's id rule: a JSON string, or a number with no fractional part.
///
/// Base JSON-RPC also permits `null`, but this revision forbids it;
/// booleans, objects, and arrays are rejected for the same reason a
/// fractional number is — none is "a string or integer".
fn valid_request_id(id: &Value) -> bool {
    match id {
        Value::String(_) => true,
        Value::Number(n) => n.as_i64().is_some() || n.as_u64().is_some(),
        _ => false,
    }
}

/// V3: decode the reserved `_meta` keys.
fn parse_meta(req: &Request) -> Result<RequestMeta, CheckError> {
    let fail = |msg: &str| CheckError {
        code: codes::INVALID_PARAMS,
        msg: msg.to_owned(),
        status: 400,
        data: None,
    };

    let meta = req.meta().ok_or_else(|| fail("missing required params._meta"))?;

    let version_raw = meta
        .get(meta_keys::PROTOCOL_VERSION)
        .cloned()
        .ok_or_else(|| {
            fail(&format!(
                "missing required _meta key: {}",
                meta_keys::PROTOCOL_VERSION
            ))
        })?;
    let capabilities = meta
        .get(meta_keys::CLIENT_CAPABILITIES)
        .cloned()
        .ok_or_else(|| {
            fail(&format!(
                "missing required _meta key: {}",
                meta_keys::CLIENT_CAPABILITIES
            ))
        })?;

    let version = version_raw.as_str().map(ToOwned::to_owned);

    // clientInfo only feeds audit metadata; a value that is not an
    // object is treated as absent rather than rejected, since V3 does
    // not require the key at all.
    let client = meta.get(meta_keys::CLIENT_INFO).and_then(Value::as_object);
    Ok(RequestMeta {
        version,
        version_raw,
        client_name: client
            .and_then(|c| c.get("name"))
            .and_then(Value::as_str)
            .map(ToOwned::to_owned),
        client_version: client
            .and_then(|c| c.get("version"))
            .and_then(Value::as_str)
            .map(ToOwned::to_owned),
        capabilities,
    })
}

/// V4: the protocol-version header must be present and equal the
/// `_meta` value.
fn validate_version_header(headers: &Headers, meta: &RequestMeta) -> Result<(), CheckError> {
    let hdr = single_header(headers, headers::PROTOCOL_VERSION).map_err(|()| CheckError {
        code: codes::HEADER_MISMATCH,
        msg: format!(
            "{} header sent with conflicting duplicate values",
            headers::PROTOCOL_VERSION
        ),
        status: 400,
        data: None,
    })?;

    let Some(hdr) = hdr.filter(|h| !h.is_empty()) else {
        return Err(CheckError {
            code: codes::HEADER_MISMATCH,
            msg: format!("missing {} header", headers::PROTOCOL_VERSION),
            status: 400,
            data: None,
        });
    };

    // A non-string _meta value can never equal the header string.
    if meta.version.as_deref() != Some(hdr) {
        let raw = render_go_value(&meta.version_raw);
        return Err(CheckError {
            code: codes::HEADER_MISMATCH,
            msg: format!(
                "{} header {:?} does not match _meta protocolVersion {raw}",
                headers::PROTOCOL_VERSION,
                hdr
            ),
            status: 400,
            data: None,
        });
    }
    Ok(())
}

/// V6: the method header must be present and equal the body method.
fn validate_method_header(headers: &Headers, req: &Request) -> Result<(), CheckError> {
    let hdr = single_header(headers, headers::METHOD).map_err(|()| CheckError {
        code: codes::HEADER_MISMATCH,
        msg: format!(
            "{} header sent with conflicting duplicate values",
            headers::METHOD
        ),
        status: 400,
        data: None,
    })?;

    let Some(hdr) = hdr.filter(|h| !h.is_empty()) else {
        return Err(CheckError {
            code: codes::HEADER_MISMATCH,
            msg: format!("missing {} header", headers::METHOD),
            status: 400,
            data: None,
        });
    };

    if hdr != req.method {
        return Err(CheckError {
            code: codes::HEADER_MISMATCH,
            msg: format!(
                "{} header {:?} does not match body method {:?}",
                headers::METHOD,
                hdr,
                req.method
            ),
            status: 400,
            data: None,
        });
    }
    Ok(())
}

/// Renders a value the way Go's `%v` verb would, for the V4 mismatch
/// message (which interpolates the raw `_meta` value unquoted).
fn render_go_value(v: &Value) -> String {
    match v {
        Value::String(s) => s.clone(),
        Value::Null => "<nil>".into(),
        Value::Bool(b) => b.to_string(),
        Value::Number(n) => n.to_string(),
        other => String::from_utf8(super::wire::to_wire_bytes(other)).unwrap_or_default(),
    }
}

/// `server/discover`: the mandatory modern discovery method.
///
/// Carries no `listChanged` flag (notifications are not implemented),
/// no `extensions` map beyond the tasks declaration, and no
/// `instructions`.
fn handle_discover(cfg: &HandlerConfig, req: &Request) -> Response {
    let mut capabilities = Map::new();
    capabilities.insert("tools".into(), Value::Object(Map::new()));

    let mut result = Map::new();
    result.insert(
        "supportedVersions".into(),
        Value::Array(vec![Value::String(PROTOCOL_VERSION.into())]),
    );
    result.insert("capabilities".into(), Value::Object(capabilities));
    tasks::declare_extension(cfg, &mut result);
    apply_cache_hints(cfg, &mut result);
    stamp_envelope(cfg, &mut result);

    result_response(200, req.id.as_ref(), &Value::Object(result))
}

/// `tools/list`: the same tool envelopes the legacy era emits, wrapped
/// in a modern complete-result with cache hints.
///
/// Pagination is not implemented: a `cursor` param is ignored and no
/// `nextCursor` is returned.
fn handle_tools_list(cfg: &HandlerConfig, bridge: &Bridge, req: &Request) -> Response {
    let tools: Vec<Value> = bridge
        .leaves()
        .iter()
        .filter(|leaf| leaf.enabled.contains(&Surface::Mcp))
        .map(super::bridge::Leaf::tool_envelope)
        .collect();

    let mut result = Map::new();
    result.insert("tools".into(), Value::Array(tools));
    apply_cache_hints(cfg, &mut result);
    stamp_envelope(cfg, &mut result);

    result_response(200, req.id.as_ref(), &Value::Object(result))
}

/// `tools/call`: V7, V9, pre-flight gates, invoke, render.
///
/// Error mapping matches the legacy era: unknown or not-enabled tool →
/// `-32602` @ 200; destructive blocks and handler failures → `isError`
/// result envelopes. `tools/call` results carry no cache hints.
fn handle_tools_call(
    cfg: &HandlerConfig,
    bridge: &Bridge,
    req: &Request,
    headers: &Headers,
    meta: &RequestMeta,
) -> Response {
    // V7 — Mcp-Name agreement, run against a pre-decode peek of
    // params.name so a header failure is reported even when the rest of
    // params is unusable.
    let name_value = req.params.as_ref().and_then(|p| p.get("name"));
    let name_present = name_value.is_some();
    let name_str = name_value.and_then(Value::as_str);
    if let Err(err) = validate_name_header(headers, name_str, name_present) {
        return write_error(req, &err);
    }

    // V9 — per-method params. Unreachable through a conforming request
    // (V7 already requires a present, non-empty, matching name), but
    // kept as the correct response if a future caller bypasses V7.
    let Some(name) = name_str.filter(|n| !n.is_empty()) else {
        return error_response(
            200,
            req.id.as_ref(),
            &ErrorObject {
                code: codes::INVALID_PARAMS,
                message: "missing tool name".into(),
                data: None,
            },
        );
    };

    let Some(leaf) = bridge.resolve_enabled(name, Surface::Mcp) else {
        return unknown_tool(cfg, req, name);
    };

    if leaf.class.auth_required && headers.get("Authorization").is_none() {
        return write_call_error(cfg, req, "authentication required", 401);
    }

    // Confirmation gate: the MRTR elicitation flow when the mount holds
    // key material and the client declares the capability, else the
    // X-Confirm-Token header gate.
    if let Some((refusal, status)) = tasks::confirmation_gate(cfg, leaf, req, headers, meta) {
        let mut result = refusal;
        stamp_envelope(cfg, &mut result);
        return result_response(status, req.id.as_ref(), &Value::Object(result));
    }

    let arguments = req
        .params
        .as_ref()
        .and_then(|p| p.get("arguments"))
        .and_then(Value::as_object)
        .cloned()
        .unwrap_or_default();

    match bridge.invoke(name, Surface::Mcp, &arguments) {
        Ok(res) => {
            let mut result = render_call_result(&res)
                .as_object()
                .cloned()
                .unwrap_or_default();
            if let Some(data) = &res.data {
                result.insert("structuredContent".into(), data.clone());
            }
            stamp_envelope(cfg, &mut result);
            result_response(200, req.id.as_ref(), &Value::Object(result))
        }
        Err(InvokeError::UnknownCommand | InvokeError::SurfaceNotEnabled) => {
            unknown_tool(cfg, req, name)
        }
        // DestructiveBlocked and every other failure: isError @ 200.
        Err(err) => write_call_error(cfg, req, &err.to_string(), 200),
    }
}

/// V7: on `tools/call` the name header must be present, non-empty after
/// Base64-sentinel decoding, and byte-equal to `params.name`, which must
/// itself be present.
///
/// A header that merely *looks* sentinel-encoded is always treated as
/// encoded, so a decode failure fails closed rather than falling back to
/// a literal comparison. When `params.name` exists but is not a string,
/// V7 has nothing to compare and defers to V9's shape check.
fn validate_name_header(
    headers: &Headers,
    name: Option<&str>,
    name_present: bool,
) -> Result<(), CheckError> {
    let hdr = single_header(headers, headers::NAME).map_err(|()| CheckError {
        code: codes::HEADER_MISMATCH,
        msg: format!(
            "{} header sent with conflicting duplicate values",
            headers::NAME
        ),
        status: 400,
        data: None,
    })?;

    let Some(hdr) = hdr.filter(|h| !h.is_empty()) else {
        return Err(CheckError {
            code: codes::HEADER_MISMATCH,
            msg: format!("missing {} header", headers::NAME),
            status: 400,
            data: None,
        });
    };

    let decoded = decode_sentinel(hdr).ok_or_else(|| CheckError {
        code: codes::HEADER_MISMATCH,
        msg: format!(
            "{} header value is not valid base64-sentinel encoded",
            headers::NAME
        ),
        status: 400,
        data: None,
    })?;

    if decoded.is_empty() {
        return Err(CheckError {
            code: codes::HEADER_MISMATCH,
            msg: format!("{} header decodes to an empty value", headers::NAME),
            status: 400,
            data: None,
        });
    }
    if !name_present {
        return Err(CheckError {
            code: codes::HEADER_MISMATCH,
            msg: format!(
                "{} header present but body params.name is absent",
                headers::NAME
            ),
            status: 400,
            data: None,
        });
    }
    // Present but not a string: V7 cannot evaluate agreement, so it
    // defers rather than guessing.
    let Some(name) = name else { return Ok(()) };

    if decoded != name {
        return Err(CheckError {
            code: codes::HEADER_MISMATCH,
            msg: format!(
                "{} header {:?} does not match body params.name {:?}",
                headers::NAME,
                decoded,
                name
            ),
            status: 400,
            data: None,
        });
    }
    Ok(())
}

/// Base64-sentinel markers, per the spec's Value Encoding section.
/// Case-sensitive and required to appear exactly.
const SENTINEL_PREFIX: &str = "=?base64?";
const SENTINEL_SUFFIX: &str = "?=";

/// Decodes a header value that may carry the Base64 sentinel.
///
/// A value that is not sentinel-wrapped is returned unchanged
/// (conforming names are header-safe ASCII and sent plain). `None`
/// signals a malformed encoding, which can never legitimately match the
/// body, so the caller fails closed.
fn decode_sentinel(value: &str) -> Option<String> {
    let Some(inner) = value
        .strip_prefix(SENTINEL_PREFIX)
        .and_then(|v| v.strip_suffix(SENTINEL_SUFFIX))
    else {
        return Some(value.to_owned());
    };
    let raw = base64_decode(inner)?;
    String::from_utf8(raw).ok()
}

/// Standard-alphabet Base64 decode with padding, matching Go's
/// `base64.StdEncoding`.
fn base64_decode(input: &str) -> Option<Vec<u8>> {
    const TABLE: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    let bytes = input.as_bytes();
    if !bytes.len().is_multiple_of(4) {
        return None;
    }
    let mut out = Vec::with_capacity(bytes.len() / 4 * 3);
    for chunk in bytes.chunks(4) {
        let mut acc: u32 = 0;
        let mut pad = 0;
        for (i, &b) in chunk.iter().enumerate() {
            let v = if b == b'=' {
                // Padding is only legal in the last two positions.
                if i < 2 {
                    return None;
                }
                pad += 1;
                0
            } else {
                if pad > 0 {
                    return None;
                }
                TABLE.iter().position(|&t| t == b)? as u32
            };
            acc = (acc << 6) | v;
        }
        let group = acc.to_be_bytes();
        out.push(group[1]);
        if pad < 2 {
            out.push(group[2]);
        }
        if pad < 1 {
            out.push(group[3]);
        }
    }
    Some(out)
}

/// Adds the members every modern result carries: `resultType` and a
/// result-level `_meta` naming the server.
///
/// A producer that already chose a `resultType` keeps it — the MRTR
/// confirmation gate is the only such producer.
pub(super) fn stamp_envelope(cfg: &HandlerConfig, result: &mut Map<String, Value>) {
    result
        .entry("resultType")
        .or_insert_with(|| Value::String(RESULT_TYPE_COMPLETE.into()));

    let mut server_info = Map::new();
    server_info.insert("name".into(), Value::String(cfg.server_name.clone()));
    server_info.insert("version".into(), Value::String(cfg.server_version.clone()));

    let mut meta = Map::new();
    meta.insert(meta_keys::SERVER_INFO.into(), Value::Object(server_info));
    result.insert("_meta".into(), Value::Object(meta));
}

/// Adds `ttlMs` and `cacheScope` to a cacheable result.
///
/// Applies to `server/discover` and `tools/list` only; `tools/call` is
/// not a cacheable operation. The defaults (`0` / `private`) are honest
/// rather than optimistic: the leaf set can mutate at runtime and no
/// `listChanged` notification exists.
fn apply_cache_hints(cfg: &HandlerConfig, result: &mut Map<String, Value>) {
    result.insert("ttlMs".into(), Value::Number(cfg.cache_ttl_ms.into()));
    result.insert(
        "cacheScope".into(),
        Value::String(cfg.cache_scope.as_str().into()),
    );
}

/// The opt-in Origin allowlist. No allowlist configured means no check;
/// a request without an Origin header is never refused.
fn origin_allowed(cfg: &HandlerConfig, headers: &Headers) -> bool {
    if cfg.origin_allowlist.is_empty() {
        return true;
    }
    match headers.get("Origin") {
        None => true,
        Some(origin) => cfg.origin_allowlist.iter().any(|a| a == origin),
    }
}

/// The `-32602` "unknown tool" response, at HTTP 200.
fn unknown_tool(_cfg: &HandlerConfig, req: &Request, name: &str) -> Response {
    error_response(
        200,
        req.id.as_ref(),
        &ErrorObject {
            code: codes::INVALID_PARAMS,
            message: format!("unknown tool: {name}"),
            data: None,
        },
    )
}

/// An `isError` `tools/call` result with the modern envelope stamped on.
fn write_call_error(cfg: &HandlerConfig, req: &Request, msg: &str, status: u16) -> Response {
    let mut result = error_result_block(msg)
        .as_object()
        .cloned()
        .unwrap_or_default();
    stamp_envelope(cfg, &mut result);
    result_response(status, req.id.as_ref(), &Value::Object(result))
}

/// Writes a modern error envelope.
///
/// When the rejected request's method is `initialize` the message
/// additionally names the supported versions: a legacy client has no
/// fall-forward mechanism, so the version list is its only recovery
/// hint (spec SHOULD for modern-only servers).
fn write_error(req: &Request, err: &CheckError) -> Response {
    let mut msg = err.msg.clone();
    if req.method == "initialize" {
        msg.push_str("; supported protocol versions: ");
        msg.push_str(PROTOCOL_VERSION);
    }
    error_response(
        err.status,
        req.id.as_ref(),
        &ErrorObject {
            code: err.code,
            message: msg,
            data: err.data.clone(),
        },
    )
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

    const META: &str = r#""_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}"#;

    #[test]
    fn protocol_version_matches_sdk() {
        // ADR 0043 §1: rmcp is the protocol layer. The const above
        // cannot reference ProtocolVersion directly (it wraps a Cow),
        // so this pins the two together instead.
        assert_eq!(
            PROTOCOL_VERSION,
            rmcp::model::ProtocolVersion::V_2026_07_28.as_str()
        );
        assert_eq!(
            crate::mcp::legacy::PROTOCOL_VERSION,
            rmcp::model::ProtocolVersion::V_2024_11_05.as_str()
        );
    }

    #[test]
    fn error_codes_match_sdk() {
        use rmcp::model::ErrorCode;
        assert_eq!(codes::HEADER_MISMATCH, i64::from(ErrorCode::HEADER_MISMATCH.0));
        assert_eq!(
            codes::UNSUPPORTED_VERSION,
            i64::from(ErrorCode::UNSUPPORTED_PROTOCOL_VERSION.0)
        );
        assert_eq!(
            codes::MISSING_CLIENT_CAPABILITY,
            i64::from(ErrorCode::MISSING_REQUIRED_CLIENT_CAPABILITY.0)
        );
    }

    #[test]
    fn notification_without_id_is_202_with_empty_body() {
        let req = parse(&format!(
            r#"{{"jsonrpc":"2.0","method":"tools/list","params":{{{META}}}}}"#
        ));
        let resp = serve(
            &HandlerConfig::default(),
            &Bridge::new(),
            &req,
            &Headers(&[]),
        );
        assert_eq!(resp.status, 202);
        assert!(resp.body.is_empty());
    }

    #[test]
    fn null_and_fractional_ids_are_rejected() {
        for bad in ["null", "1.5", "true", "[]", "{}"] {
            let req = parse(&format!(
                r#"{{"jsonrpc":"2.0","id":{bad},"method":"tools/list","params":{{{META}}}}}"#
            ));
            let resp = serve(
                &HandlerConfig::default(),
                &Bridge::new(),
                &req,
                &Headers(&[]),
            );
            assert_eq!(resp.status, 400, "id {bad} must be rejected");
            assert!(resp.body_str().contains("-32600"));
        }
        // Integers and strings are fine.
        for good in ["1", "\"abc\"", "-4"] {
            let req = parse(&format!(
                r#"{{"jsonrpc":"2.0","id":{good},"method":"tools/list","params":{{{META}}}}}"#
            ));
            let headers = hdrs(&[
                ("MCP-Protocol-Version", "2026-07-28"),
                ("Mcp-Method", "tools/list"),
            ]);
            let resp = serve(
                &HandlerConfig::default(),
                &Bridge::new(),
                &req,
                &Headers(&headers),
            );
            assert_eq!(resp.status, 200, "id {good} must be accepted");
        }
    }

    #[test]
    fn missing_meta_keys_are_32602_at_400() {
        // No _meta at all.
        let req = parse(r#"{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}"#);
        let resp = serve(
            &HandlerConfig::default(),
            &Bridge::new(),
            &req,
            &Headers(&[]),
        );
        assert_eq!(resp.status, 400);
        assert!(resp.body_str().contains("missing required params._meta"));

        // protocolVersion present but clientCapabilities missing.
        let req = parse(
            r#"{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}"#,
        );
        let resp = serve(
            &HandlerConfig::default(),
            &Bridge::new(),
            &req,
            &Headers(&[]),
        );
        assert_eq!(resp.status, 400);
        assert!(resp
            .body_str()
            .contains("io.modelcontextprotocol/clientCapabilities"));
    }

    #[test]
    fn missing_protocol_version_header_is_32020() {
        let req = parse(&format!(
            r#"{{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{{{META}}}}}"#
        ));
        let resp = serve(
            &HandlerConfig::default(),
            &Bridge::new(),
            &req,
            &Headers(&[]),
        );
        assert_eq!(resp.status, 400);
        assert!(resp.body_str().contains("-32020"));
        assert!(resp.body_str().contains("missing MCP-Protocol-Version"));
    }

    #[test]
    fn conflicting_duplicate_headers_are_rejected_without_comparison() {
        let req = parse(&format!(
            r#"{{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{{{META}}}}}"#
        ));
        // Two differing values: rejected even though one of them is
        // the correct value.
        let headers = hdrs(&[
            ("MCP-Protocol-Version", "2026-07-28"),
            ("MCP-Protocol-Version", "2024-11-05"),
        ]);
        let resp = serve(
            &HandlerConfig::default(),
            &Bridge::new(),
            &req,
            &Headers(&headers),
        );
        assert_eq!(resp.status, 400);
        assert!(resp.body_str().contains("conflicting duplicate values"));

        // Byte-identical duplicates are tolerated.
        let headers = hdrs(&[
            ("MCP-Protocol-Version", "2026-07-28"),
            ("MCP-Protocol-Version", "2026-07-28"),
            ("Mcp-Method", "tools/list"),
        ]);
        let resp = serve(
            &HandlerConfig::default(),
            &Bridge::new(),
            &req,
            &Headers(&headers),
        );
        assert_eq!(resp.status, 200);
    }

    #[test]
    fn unknown_method_is_404_unlike_legacys_200() {
        let req = parse(&format!(
            r#"{{"jsonrpc":"2.0","id":1,"method":"nope","params":{{{META}}}}}"#
        ));
        let headers = hdrs(&[
            ("MCP-Protocol-Version", "2026-07-28"),
            ("Mcp-Method", "nope"),
        ]);
        let resp = serve(
            &HandlerConfig::default(),
            &Bridge::new(),
            &req,
            &Headers(&headers),
        );
        assert_eq!(resp.status, 404);
        assert!(resp.body_str().contains("-32601"));
    }

    #[test]
    fn name_header_accepts_base64_sentinel_and_rejects_empty_sentinel() {
        let bridge = Bridge::new().leaf(Leaf::new(&["ping"], "Ping", |_| {
            Ok(CallResult::ok("pong\n"))
        }));
        // "ping" base64-encoded.
        let headers = hdrs(&[
            ("MCP-Protocol-Version", "2026-07-28"),
            ("Mcp-Method", "tools/call"),
            ("Mcp-Name", "=?base64?cGluZw==?="),
        ]);
        let req = parse(&format!(
            r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"ping",{META}}}}}"#
        ));
        let resp = serve(&HandlerConfig::default(), &bridge, &req, &Headers(&headers));
        assert_eq!(resp.status, 200, "sentinel-encoded name must match");

        // The empty sentinel decodes to "" and counts as empty.
        let headers = hdrs(&[
            ("MCP-Protocol-Version", "2026-07-28"),
            ("Mcp-Method", "tools/call"),
            ("Mcp-Name", "=?base64??="),
        ]);
        let resp = serve(&HandlerConfig::default(), &bridge, &req, &Headers(&headers));
        assert_eq!(resp.status, 400);
        assert!(resp.body_str().contains("decodes to an empty value"));
    }

    #[test]
    fn base64_decode_matches_go_std_encoding() {
        assert_eq!(base64_decode("cGluZw==").unwrap(), b"ping");
        assert_eq!(base64_decode("").unwrap(), b"");
        assert_eq!(base64_decode("YQ==").unwrap(), b"a");
        assert_eq!(base64_decode("YWI=").unwrap(), b"ab");
        assert_eq!(base64_decode("YWJj").unwrap(), b"abc");
        // Unpadded and malformed inputs fail closed.
        assert!(base64_decode("cGluZw").is_none());
        assert!(base64_decode("!!!!").is_none());
    }

    #[test]
    fn tools_call_carries_no_cache_hints() {
        let bridge = Bridge::new().leaf(Leaf::new(&["ping"], "Ping", |_| {
            Ok(CallResult::ok("pong\n"))
        }));
        let headers = hdrs(&[
            ("MCP-Protocol-Version", "2026-07-28"),
            ("Mcp-Method", "tools/call"),
            ("Mcp-Name", "ping"),
        ]);
        let req = parse(&format!(
            r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"ping",{META}}}}}"#
        ));
        let resp = serve(&HandlerConfig::default(), &bridge, &req, &Headers(&headers));
        assert!(!resp.body_str().contains("ttlMs"));
        assert!(!resp.body_str().contains("cacheScope"));
    }

    #[test]
    fn initialize_rejection_names_supported_versions() {
        // Reachable on a modern-only mount, where initialize is not
        // demoted to legacy.
        let req = parse(r#"{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}"#);
        let resp = serve(
            &HandlerConfig::default(),
            &Bridge::new(),
            &req,
            &Headers(&[]),
        );
        assert!(resp
            .body_str()
            .contains("supported protocol versions: 2026-07-28"));
    }

    #[test]
    fn origin_allowlist_refuses_unlisted_origin() {
        let cfg = HandlerConfig {
            origin_allowlist: vec!["https://ok.example".into()],
            ..HandlerConfig::default()
        };
        let req = parse(&format!(
            r#"{{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{{{META}}}}}"#
        ));
        let headers = hdrs(&[("Origin", "https://evil.example")]);
        let resp = serve(&cfg, &Bridge::new(), &req, &Headers(&headers));
        assert_eq!(resp.status, 403);

        // A request with no Origin at all is never refused.
        let headers = hdrs(&[
            ("MCP-Protocol-Version", "2026-07-28"),
            ("Mcp-Method", "tools/list"),
        ]);
        let resp = serve(&cfg, &Bridge::new(), &req, &Headers(&headers));
        assert_eq!(resp.status, 200);
    }
}
