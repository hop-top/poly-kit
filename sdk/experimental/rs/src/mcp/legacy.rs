//! The 2024-11-05 handler.
//!
//! This module is **frozen**: it reproduces Go's original `mcpHandler`
//! exactly, and the dual-spec work is additive only. ADR 0043 §7 makes
//! the three-way split load-bearing precisely so a reviewer can confirm
//! a modern-era change did not reach in here.
//!
//! Methods served: `initialize`, `tools/list`, `tools/call`. Anything
//! else is `-32601` at HTTP **200** — note the status: the legacy era
//! rides application errors on 200, and the fixtures pin that.

use serde_json::{Map, Value};

use super::bridge::{Bridge, CallResult, InvokeError};
use super::safety::Surface;
use super::wire::{codes, error_response, result_response, ErrorObject, Request, Response};
use super::HandlerConfig;

/// The protocol revision this handler speaks.
pub const PROTOCOL_VERSION: &str = "2024-11-05";

/// Serves one already-parsed request on the legacy era.
pub(super) fn serve(
    cfg: &HandlerConfig,
    bridge: &Bridge,
    req: &Request,
    headers: &Headers,
) -> Response {
    if !req.jsonrpc_ok() {
        return error_response(
            400,
            req.id.as_ref(),
            &ErrorObject {
                code: codes::INVALID_REQUEST,
                message: "invalid jsonrpc version".into(),
                data: None,
            },
        );
    }

    match req.method.as_str() {
        "initialize" => handle_initialize(cfg, req),
        "tools/list" => handle_tools_list(bridge, req),
        "tools/call" => handle_tools_call(bridge, req, headers),
        other => error_response(
            // HTTP 200, not 404: the legacy era's convention, preserved.
            200,
            req.id.as_ref(),
            &ErrorObject {
                code: codes::METHOD_NOT_FOUND,
                message: format!("method not found: {other}"),
                data: None,
            },
        ),
    }
}

/// Header lookup for the pre-flight gates. Case-insensitive per
/// RFC 9110.
pub(super) struct Headers<'a>(pub &'a [(String, String)]);

impl Headers<'_> {
    /// The first value for `name`, or `None`.
    pub(super) fn get(&self, name: &str) -> Option<&str> {
        self.0
            .iter()
            .find(|(k, _)| k.eq_ignore_ascii_case(name))
            .map(|(_, v)| v.as_str())
    }

    /// Every value for `name`, in order.
    pub(super) fn all(&self, name: &str) -> Vec<&str> {
        self.0
            .iter()
            .filter(|(k, _)| k.eq_ignore_ascii_case(name))
            .map(|(_, v)| v.as_str())
            .collect()
    }
}

/// `initialize`: protocol version, capabilities, server identity.
fn handle_initialize(cfg: &HandlerConfig, req: &Request) -> Response {
    let mut capabilities = Map::new();
    capabilities.insert("tools".into(), Value::Object(Map::new()));

    let mut server_info = Map::new();
    server_info.insert("name".into(), Value::String(cfg.server_name.clone()));
    server_info.insert("version".into(), Value::String(cfg.server_version.clone()));

    let mut result = Map::new();
    result.insert(
        "protocolVersion".into(),
        Value::String(PROTOCOL_VERSION.into()),
    );
    result.insert("capabilities".into(), Value::Object(capabilities));
    result.insert("serverInfo".into(), Value::Object(server_info));

    result_response(200, req.id.as_ref(), &Value::Object(result))
}

/// `tools/list`: one envelope per MCP-enabled leaf.
fn handle_tools_list(bridge: &Bridge, req: &Request) -> Response {
    let tools: Vec<Value> = bridge
        .leaves()
        .iter()
        .filter(|leaf| leaf.enabled.contains(&Surface::Mcp))
        .map(super::bridge::Leaf::tool_envelope)
        .collect();

    let mut result = Map::new();
    result.insert("tools".into(), Value::Array(tools));
    result_response(200, req.id.as_ref(), &Value::Object(result))
}

/// `tools/call`: resolve, gate, invoke, render.
fn handle_tools_call(bridge: &Bridge, req: &Request, headers: &Headers) -> Response {
    let params = req.params.as_ref();
    let name = params
        .and_then(|p| p.get("name"))
        .and_then(Value::as_str)
        .unwrap_or_default();

    if name.is_empty() {
        return error_response(
            200,
            req.id.as_ref(),
            &ErrorObject {
                code: codes::INVALID_PARAMS,
                message: "missing tool name".into(),
                data: None,
            },
        );
    }

    let Some(leaf) = bridge.resolve_enabled(name, Surface::Mcp) else {
        return unknown_tool(req, name);
    };

    // Pre-flight gates: mirrored on the result envelope so MCP-aware
    // clients see isError while HTTP-only clients see the status code.
    if leaf.class.auth_required && headers.get("Authorization").is_none() {
        return result_response(
            401,
            req.id.as_ref(),
            &error_result_block("authentication required"),
        );
    }
    if leaf.class.requires_confirmation && headers.get("X-Confirm-Token").is_none() {
        return result_response(
            428,
            req.id.as_ref(),
            &error_result_block("confirmation required"),
        );
    }

    let arguments = params
        .and_then(|p| p.get("arguments"))
        .and_then(Value::as_object)
        .cloned()
        .unwrap_or_default();

    match bridge.invoke(name, Surface::Mcp, &arguments) {
        Ok(res) => result_response(200, req.id.as_ref(), &render_call_result(&res)),
        Err(InvokeError::UnknownCommand | InvokeError::SurfaceNotEnabled) => {
            unknown_tool(req, name)
        }
        // DestructiveBlocked and every other failure alike: an isError
        // result at HTTP 200, never a transport error.
        Err(err) => result_response(200, req.id.as_ref(), &error_result_block(&err.to_string())),
    }
}

/// The `-32602` "unknown tool" response, at HTTP 200.
fn unknown_tool(req: &Request, name: &str) -> Response {
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

/// Maps a [`CallResult`] to the `tools/call` result envelope.
///
/// The content list always holds at least the stdout block (possibly
/// empty); stderr and structured data each append one more. Shared with
/// the modern era, which wraps the same blocks in its own envelope.
pub(super) fn render_call_result(res: &CallResult) -> Value {
    let mut content = vec![text_block(&res.stdout)];
    if !res.stderr.is_empty() {
        content.push(text_block(&format!("[stderr] {}", res.stderr)));
    }
    if let Some(data) = &res.data {
        // Serialized with sorted keys, as Go's json.Marshal would.
        let encoded = String::from_utf8(super::wire::to_wire_bytes(data)).unwrap_or_default();
        content.push(text_block(&encoded));
    }

    let mut out = Map::new();
    out.insert("content".into(), Value::Array(content));
    out.insert("isError".into(), Value::Bool(res.exit_code != 0));
    Value::Object(out)
}

/// An `isError` result carrying a single text block.
pub(super) fn error_result_block(msg: &str) -> Value {
    let mut out = Map::new();
    out.insert("content".into(), Value::Array(vec![text_block(msg)]));
    out.insert("isError".into(), Value::Bool(true));
    Value::Object(out)
}

/// One `{"type":"text","text":...}` content block.
fn text_block(text: &str) -> Value {
    let mut block = Map::new();
    block.insert("type".into(), Value::String("text".into()));
    block.insert("text".into(), Value::String(text.to_owned()));
    Value::Object(block)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mcp::bridge::Leaf;
    use crate::mcp::safety::SafetyClass;

    fn cfg() -> HandlerConfig {
        HandlerConfig::default()
    }

    fn parse(body: &str) -> Request {
        Request::from_slice(body.as_bytes()).unwrap()
    }

    #[test]
    fn unknown_method_is_32601_at_http_200_not_404() {
        let bridge = Bridge::new();
        let resp = serve(
            &cfg(),
            &bridge,
            &parse(r#"{"jsonrpc":"2.0","id":6,"method":"nope"}"#),
            &Headers(&[]),
        );
        assert_eq!(resp.status, 200, "legacy rides application errors on 200");
        assert_eq!(
            resp.body_str(),
            "{\"jsonrpc\":\"2.0\",\"id\":6,\"error\":{\"code\":-32601,\
             \"message\":\"method not found: nope\"}}\n"
        );
    }

    #[test]
    fn auth_required_leaf_without_header_is_401() {
        let bridge = Bridge::new().leaf(
            Leaf::new(&["secret"], "Locked", |_| Ok(CallResult::default())).with_class(
                SafetyClass {
                    auth_required: true,
                    ..SafetyClass::default()
                },
            ),
        );
        let req =
            parse(r#"{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"secret"}}"#);
        let resp = serve(&cfg(), &bridge, &req, &Headers(&[]));
        assert_eq!(resp.status, 401);
        assert!(resp.body_str().contains("authentication required"));

        // With the header present the gate opens.
        let headers = [("Authorization".to_string(), "Bearer x".to_string())];
        let resp = serve(&cfg(), &bridge, &req, &Headers(&headers));
        assert_eq!(resp.status, 200);
    }

    #[test]
    fn confirmation_required_leaf_without_token_is_428() {
        let bridge = Bridge::new().leaf(
            Leaf::new(&["deploy"], "Deploy", |_| Ok(CallResult::default())).with_class(
                SafetyClass {
                    requires_confirmation: true,
                    ..SafetyClass::default()
                },
            ),
        );
        let resp = serve(
            &cfg(),
            &bridge,
            &parse(r#"{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deploy"}}"#),
            &Headers(&[]),
        );
        assert_eq!(resp.status, 428);
        assert!(resp.body_str().contains("confirmation required"));
    }

    #[test]
    fn stderr_and_data_append_content_blocks() {
        let res = CallResult {
            stdout: "out".into(),
            stderr: "bad".into(),
            exit_code: 1,
            data: Some(serde_json::json!({"b": 1, "a": 2})),
        };
        let rendered = render_call_result(&res);
        let content = rendered["content"].as_array().unwrap();
        assert_eq!(content.len(), 3);
        assert_eq!(content[0]["text"], "out");
        assert_eq!(content[1]["text"], "[stderr] bad");
        // The JSON block is key-sorted, as Go's json.Marshal emits it.
        assert_eq!(content[2]["text"], r#"{"a":2,"b":1}"#);
        assert_eq!(rendered["isError"], Value::Bool(true));
    }

    #[test]
    fn missing_tool_name_is_32602_at_200() {
        let resp = serve(
            &cfg(),
            &Bridge::new(),
            &parse(r#"{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}"#),
            &Headers(&[]),
        );
        assert_eq!(resp.status, 200);
        assert!(resp.body_str().contains("missing tool name"));
    }

    #[test]
    fn headers_lookup_is_case_insensitive() {
        let headers = [("authorization".to_string(), "Bearer x".to_string())];
        assert_eq!(Headers(&headers).get("Authorization"), Some("Bearer x"));
    }
}
