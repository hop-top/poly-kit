//! JSON-RPC envelopes and the byte-exact serializer.
//!
//! # Why this module hand-rolls serialization
//!
//! The parity contract is byte-identical output against Go
//! (`sdk/tests/cross-lang/fixtures/mcp-wire.json`), and Go's
//! `encoding/json` has two properties this crate does not get for free:
//!
//! 1. **`map[string]any` keys are emitted lexicographically.** This
//!    crate enables serde_json's `preserve_order`, which backs
//!    [`serde_json::Map`] with an `IndexMap` — insertion order, not
//!    sorted order. Emitting a map built in a natural order would put
//!    the keys in the wrong sequence. [`to_wire_bytes`] therefore sorts
//!    every object it writes, at every depth.
//! 2. **Struct fields are emitted in declaration order.** The top-level
//!    envelope is a Go *struct* (`jsonrpc`, `id`, `result`/`error`) and
//!    the error object is a struct (`code`, `message`, `data`), so those
//!    two shapes are *not* sorted — `jsonrpc` precedes `id`, and `code`
//!    precedes `message`. Sorting them would be just as wrong as failing
//!    to sort the maps.
//!
//! Both rules are exercised by the fixtures: `modern/server-discover`
//! pins the sorted-map rule and every case pins the struct-order rule.
//!
//! Go also terminates each response with a newline (`json.Encoder`
//! appends one); [`to_wire_bytes`] reproduces that.

use serde_json::{Map, Value};

/// JSON-RPC error codes this surface emits.
///
/// Every value is taken from `rmcp::model::ErrorCode` rather than
/// spelled locally: ADR 0043 §1 fixes the official SDK as the protocol
/// layer, so the `-3202x` codes reserved by MCP 2026-07-28 and the
/// classic JSON-RPC range have exactly one source of truth.
pub mod codes {
    use rmcp::model::ErrorCode;

    /// Malformed JSON in the request body.
    pub const PARSE: i64 = ErrorCode::PARSE_ERROR.0 as i64;
    /// Envelope is not a valid request.
    pub const INVALID_REQUEST: i64 = ErrorCode::INVALID_REQUEST.0 as i64;
    /// Unknown method.
    pub const METHOD_NOT_FOUND: i64 = ErrorCode::METHOD_NOT_FOUND.0 as i64;
    /// Bad or missing params.
    pub const INVALID_PARAMS: i64 = ErrorCode::INVALID_PARAMS.0 as i64;
    /// Server-side failure.
    pub const INTERNAL: i64 = ErrorCode::INTERNAL_ERROR.0 as i64;
    /// A routing header disagrees with the body (2026-07-28).
    pub const HEADER_MISMATCH: i64 = ErrorCode::HEADER_MISMATCH.0 as i64;
    /// A required client capability was not declared (2026-07-28).
    pub const MISSING_CLIENT_CAPABILITY: i64 =
        ErrorCode::MISSING_REQUIRED_CLIENT_CAPABILITY.0 as i64;
    /// The requested protocol version is not served (2026-07-28).
    pub const UNSUPPORTED_VERSION: i64 = ErrorCode::UNSUPPORTED_PROTOCOL_VERSION.0 as i64;
}

/// A decoded JSON-RPC request envelope.
///
/// `id` and `params` stay as raw [`Value`]s: `id` round-trips verbatim
/// (including `null`, which the legacy era echoes back), and `params` is
/// interpreted differently per era, so neither is decoded here.
#[derive(Debug, Clone)]
pub struct Request {
    /// The `jsonrpc` member. Absent is tolerated and stored as `None`.
    pub jsonrpc: Option<String>,
    /// The `id` member, verbatim. `None` marks a notification.
    pub id: Option<Value>,
    /// The `method` member. Absent decodes to an empty string, matching
    /// Go's zero-value `string` field.
    pub method: String,
    /// The `params` member, undecoded.
    pub params: Option<Value>,
}

impl Request {
    /// Decodes a request envelope from raw body bytes.
    ///
    /// Returns the serde error on malformed JSON so the caller can
    /// render `-32700` with Go's exact message text.
    pub fn from_slice(body: &[u8]) -> Result<Self, serde_json::Error> {
        let value: Value = serde_json::from_slice(body)?;
        let obj = match value {
            Value::Object(map) => map,
            // A valid-JSON non-object (e.g. `[]`, `3`) leaves every
            // member absent, mirroring Go's decode into a struct, which
            // errors — but Go's error surfaces as -32700 too, and the
            // dispatcher renders it identically.
            other => {
                return Err(serde::de::Error::custom(format!(
                    "json: cannot unmarshal {} into Go value of type \
                     cmdsurface.jsonRPCRequest",
                    json_kind(&other)
                )))
            }
        };
        Ok(Self {
            jsonrpc: obj
                .get("jsonrpc")
                .and_then(Value::as_str)
                .map(ToOwned::to_owned),
            id: obj.get("id").cloned(),
            method: obj
                .get("method")
                .and_then(Value::as_str)
                .unwrap_or_default()
                .to_owned(),
            params: obj.get("params").cloned(),
        })
    }

    /// Reports whether the `jsonrpc` member is acceptable: absent or
    /// exactly `"2.0"` (the same tolerance the legacy handler has).
    #[must_use]
    pub fn jsonrpc_ok(&self) -> bool {
        match &self.jsonrpc {
            None => true,
            Some(v) => v.is_empty() || v == "2.0",
        }
    }

    /// The `params._meta` object, when params is an object carrying an
    /// object-valued `_meta`.
    #[must_use]
    pub fn meta(&self) -> Option<&Map<String, Value>> {
        self.params.as_ref()?.get("_meta")?.as_object()
    }
}

/// Names a JSON value's type the way Go's decoder does, for the
/// `-32700` message on a valid-JSON non-object body.
fn json_kind(v: &Value) -> &'static str {
    match v {
        Value::Null => "null",
        Value::Bool(_) => "bool",
        Value::Number(_) => "number",
        Value::String(_) => "string",
        Value::Array(_) => "array",
        Value::Object(_) => "object",
    }
}

/// A JSON-RPC error object: `code`, `message`, `data` — in that order,
/// because Go emits it from a struct rather than a map.
#[derive(Debug, Clone)]
pub struct ErrorObject {
    /// The numeric error code.
    pub code: i64,
    /// Human-readable message.
    pub message: String,
    /// Optional structured payload. Omitted from the wire when `None`.
    pub data: Option<Value>,
}

/// One HTTP response: status plus the exact body bytes to write.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Response {
    /// HTTP status code.
    pub status: u16,
    /// `Content-Type` value, when the response carries a body.
    pub content_type: Option<&'static str>,
    /// The response body, byte-exact.
    pub body: Vec<u8>,
}

impl Response {
    /// A JSON response carrying `body`.
    #[must_use]
    pub fn json(status: u16, body: Vec<u8>) -> Self {
        Self {
            status,
            content_type: Some("application/json"),
            body,
        }
    }

    /// An empty-bodied response (HTTP 202 for notifications).
    #[must_use]
    pub fn empty(status: u16) -> Self {
        Self {
            status,
            content_type: None,
            body: Vec::new(),
        }
    }

    /// The body as UTF-8, for assertions and logging.
    #[must_use]
    pub fn body_str(&self) -> &str {
        std::str::from_utf8(&self.body).unwrap_or_default()
    }
}

/// Serializes a successful JSON-RPC result envelope.
///
/// Emits `{"jsonrpc":"2.0","id":<id>,"result":<result>}` — struct order
/// at the top level, sorted keys inside `result`. `id` is omitted when
/// `None` (Go's `omitempty` on a nil `json.RawMessage`) but emitted as
/// `null` when it is `Some(Value::Null)`, which is how the legacy era
/// echoes a null id back.
#[must_use]
pub fn result_response(status: u16, id: Option<&Value>, result: &Value) -> Response {
    let mut out = Vec::with_capacity(256);
    out.extend_from_slice(br#"{"jsonrpc":"2.0""#);
    if let Some(id) = id {
        out.extend_from_slice(br#","id":"#);
        write_sorted(&mut out, id);
    }
    out.extend_from_slice(br#","result":"#);
    write_sorted(&mut out, result);
    out.push(b'}');
    out.push(b'\n');
    Response::json(status, out)
}

/// Serializes a JSON-RPC error envelope.
///
/// Emits `{"jsonrpc":"2.0","id":<id>,"error":{"code":..,"message":..,
/// "data":..}}`. Both the envelope and the error object keep struct
/// order; `data` is omitted when `None`.
#[must_use]
pub fn error_response(status: u16, id: Option<&Value>, err: &ErrorObject) -> Response {
    let mut out = Vec::with_capacity(192);
    out.extend_from_slice(br#"{"jsonrpc":"2.0""#);
    if let Some(id) = id {
        out.extend_from_slice(br#","id":"#);
        write_sorted(&mut out, id);
    }
    out.extend_from_slice(br#","error":{"code":"#);
    out.extend_from_slice(err.code.to_string().as_bytes());
    out.extend_from_slice(br#","message":"#);
    write_sorted(&mut out, &Value::String(err.message.clone()));
    if let Some(data) = &err.data {
        out.extend_from_slice(br#","data":"#);
        write_sorted(&mut out, data);
    }
    out.extend_from_slice(b"}}\n");
    Response::json(status, out)
}

/// Serializes any [`Value`] with every object's keys in lexicographic
/// (byte) order, at every depth.
///
/// This is the counterpart to Go's `encoding/json` map handling. Rust's
/// `str` ordering is byte-wise, the same comparison Go's `sort.Strings`
/// applies to map keys, so the two agree for every key this surface
/// emits (verified against Go for the full key set in the fixtures).
#[must_use]
pub fn to_wire_bytes(v: &Value) -> Vec<u8> {
    let mut out = Vec::with_capacity(256);
    write_sorted(&mut out, v);
    out
}

/// Writes `v` into `out`, sorting object keys at every depth.
fn write_sorted(out: &mut Vec<u8>, v: &Value) {
    match v {
        Value::Object(map) => {
            let mut keys: Vec<&String> = map.keys().collect();
            keys.sort_unstable();
            out.push(b'{');
            for (i, key) in keys.iter().enumerate() {
                if i > 0 {
                    out.push(b',');
                }
                // Scalars are delegated to serde_json so string escaping
                // matches Go's (both emit the JSON-standard escapes for
                // the ASCII control set and the quote/backslash pair).
                let encoded = serde_json::to_vec(&Value::String((*key).clone()))
                    .expect("string always serializes");
                out.extend_from_slice(&encoded);
                out.push(b':');
                write_sorted(out, &map[*key]);
            }
            out.push(b'}');
        }
        Value::Array(items) => {
            out.push(b'[');
            for (i, item) in items.iter().enumerate() {
                if i > 0 {
                    out.push(b',');
                }
                write_sorted(out, item);
            }
            out.push(b']');
        }
        scalar => {
            let encoded = serde_json::to_vec(scalar).expect("scalar always serializes");
            out.extend_from_slice(&encoded);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn objects_are_emitted_in_sorted_key_order() {
        // Built in deliberately non-sorted insertion order. With
        // preserve_order active, a naive serialization would emit them
        // in exactly this order; the wire writer must not.
        let mut map = Map::new();
        map.insert("ttlMs".into(), json!(0));
        map.insert("_meta".into(), json!({}));
        map.insert("cacheScope".into(), json!("private"));
        let bytes = to_wire_bytes(&Value::Object(map));
        assert_eq!(
            String::from_utf8(bytes).unwrap(),
            r#"{"_meta":{},"cacheScope":"private","ttlMs":0}"#
        );
    }

    #[test]
    fn sorting_recurses_through_arrays_and_nested_objects() {
        let mut inner = Map::new();
        inner.insert("z".into(), json!(1));
        inner.insert("a".into(), json!(2));
        let value = json!({ "tools": [Value::Object(inner)] });
        assert_eq!(
            String::from_utf8(to_wire_bytes(&value)).unwrap(),
            r#"{"tools":[{"a":2,"z":1}]}"#
        );
    }

    #[test]
    fn preserve_order_is_active_so_sorting_is_load_bearing() {
        // Guards the assumption the whole module rests on: if this
        // crate ever dropped `preserve_order`, serde_json would sort on
        // its own and a regression in write_sorted would go unnoticed.
        let mut map = Map::new();
        map.insert("z".into(), json!(1));
        map.insert("a".into(), json!(2));
        let naive = serde_json::to_string(&Value::Object(map)).unwrap();
        assert_eq!(
            naive, r#"{"z":1,"a":2}"#,
            "serde_json must be preserving insertion order for the \
             explicit sort in write_sorted to be necessary"
        );
    }

    #[test]
    fn result_envelope_keeps_struct_field_order() {
        // jsonrpc before id before result — Go emits this from a
        // struct, so it is NOT sorted.
        let resp = result_response(200, Some(&json!(1)), &json!({"b": 1, "a": 2}));
        assert_eq!(
            resp.body_str(),
            "{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"a\":2,\"b\":1}}\n"
        );
    }

    #[test]
    fn result_envelope_emits_null_id_verbatim_and_omits_absent_id() {
        let with_null = result_response(200, Some(&Value::Null), &json!({}));
        assert_eq!(
            with_null.body_str(),
            "{\"jsonrpc\":\"2.0\",\"id\":null,\"result\":{}}\n"
        );
        let without = result_response(200, None, &json!({}));
        assert_eq!(without.body_str(), "{\"jsonrpc\":\"2.0\",\"result\":{}}\n");
    }

    #[test]
    fn error_envelope_keeps_code_message_data_order() {
        let resp = error_response(
            400,
            Some(&json!(5)),
            &ErrorObject {
                code: codes::UNSUPPORTED_VERSION,
                message: "unsupported protocol version: 2099-01-01".into(),
                data: Some(json!({"supported": ["2026-07-28"], "requested": "2099-01-01"})),
            },
        );
        // code, then message, then data (struct order); but the data
        // payload itself is a map and therefore sorted.
        assert_eq!(
            resp.body_str(),
            "{\"jsonrpc\":\"2.0\",\"id\":5,\"error\":{\"code\":-32022,\
             \"message\":\"unsupported protocol version: 2099-01-01\",\
             \"data\":{\"requested\":\"2099-01-01\",\"supported\":[\"2026-07-28\"]}}}\n"
        );
    }

    #[test]
    fn error_envelope_omits_absent_data() {
        let resp = error_response(
            200,
            Some(&json!(6)),
            &ErrorObject {
                code: codes::METHOD_NOT_FOUND,
                message: "method not found: nope".into(),
                data: None,
            },
        );
        assert_eq!(
            resp.body_str(),
            "{\"jsonrpc\":\"2.0\",\"id\":6,\"error\":{\"code\":-32601,\
             \"message\":\"method not found: nope\"}}\n"
        );
    }

    #[test]
    fn every_response_ends_with_a_newline() {
        assert!(result_response(200, None, &json!({})).body.ends_with(b"\n"));
        assert!(error_response(
            400,
            None,
            &ErrorObject {
                code: codes::PARSE,
                message: "x".into(),
                data: None
            }
        )
        .body
        .ends_with(b"\n"));
    }

    #[test]
    fn request_tolerates_absent_jsonrpc_and_rejects_other_versions() {
        let req = Request::from_slice(br#"{"id":1,"method":"tools/list"}"#).unwrap();
        assert!(req.jsonrpc_ok());
        let req = Request::from_slice(br#"{"jsonrpc":"1.0","method":"x"}"#).unwrap();
        assert!(!req.jsonrpc_ok());
    }

    #[test]
    fn request_keeps_null_id_distinct_from_absent_id() {
        let null_id = Request::from_slice(br#"{"id":null,"method":"initialize"}"#).unwrap();
        assert_eq!(null_id.id, Some(Value::Null));
        let no_id = Request::from_slice(br#"{"method":"initialize"}"#).unwrap();
        assert_eq!(no_id.id, None);
    }
}
