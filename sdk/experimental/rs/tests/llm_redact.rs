//! The bus content opt-in's redaction helpers (Go `RedactRequest`,
//! `RedactResponse`): every text masked, tool-call arguments masked on
//! their decoded JSON and failing closed to a fixed placeholder.

#![cfg(all(feature = "llm", feature = "redact"))]

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

use hop_top_kit::bus::DynBus;
use hop_top_kit::llm::{
    redact_request, redact_response, BoxFuture, Capability, Client, LlmError, Media, MediaKind,
    Message, Part, Provider, Request, ThinkingBlock, ToolCall, ToolDef, ToolResponse,
};
use hop_top_kit::redact::{self, Redactor};
use serde_json::Value;

fn call(args: &str) -> Request {
    Request {
        messages: vec![Message::assistant_calls(
            "",
            vec![ToolCall {
                id: "c".into(),
                name: "t".into(),
                arguments: args.into(),
                ..ToolCall::default()
            }],
        )],
        ..Request::default()
    }
}

fn redacted_args(r: Redactor, args: &str) -> String {
    let out = redact_request(r)(call(args));
    out.messages[0].tool_calls[0].arguments.clone()
}

/// Every string (keys included) and number in `v`, NUL-separated.
fn texts(v: &Value) -> String {
    match v {
        Value::String(s) => format!("{s}\0"),
        Value::Number(n) => format!("{n}\0"),
        Value::Array(a) => a.iter().map(texts).collect(),
        Value::Object(o) => o
            .iter()
            .map(|(k, e)| format!("{k}\0{}", texts(e)))
            .collect(),
        _ => String::new(),
    }
}

/// A secret whose JSON form needs escapes (quote, backslash, a \u
/// escape, a newline) is matched on its decoded text, not the wire form.
#[test]
fn arguments_match_decoded_strings() {
    for (name, args) in [
        ("escaped value", r#"{"pw":"p\"w\\d\n<é>"}"#),
        ("escaped key", r#"{"p\"w\\d\n<é>":1}"#),
        ("nested", r#"{"a":[{"b":"x secret-key y"}]}"#),
        ("not JSON", r#"{"pw": secret-key"#),
    ] {
        let mut r = Redactor::new();
        r.add_rule("pw", r#"p"w\\d\n<é>"#, "").unwrap();
        r.add_rule("key", "secret-key", "").unwrap();
        let got = redacted_args(r, args);
        let v: Value = serde_json::from_str(&got).unwrap_or_else(|e| panic!("{name}: {got}: {e}"));
        let t = texts(&v);
        assert!(!t.contains("secret-key"), "{name}: {got}");
        assert!(!t.contains("p\"w\\d\n<é>"), "{name}: {got}");
        assert!(got.contains("REDACTED"), "{name}: {got}");
    }
}

/// Rules that need the key beside the value (gitleaks generic-api-key, a
/// keyword-gated custom rule) still mask a value split from its key;
/// numbers are masked on their text.
#[test]
fn arguments_key_context() {
    const API_KEY: &str = "Zq8Lm3Np7Rt2Vx9Kb4Hc6Jd1Fg5";
    let generic = format!(r#"{{"api_key":"{API_KEY}"}}"#);
    let nested = format!(r#"{{"cfg":[{{"api_key":"{API_KEY}"}}]}}"#);
    for (name, args, secret) in [
        ("generic api key", generic.as_str(), API_KEY),
        ("nested generic api key", nested.as_str(), API_KEY),
        (
            "keyword-gated custom rule",
            r#"{"secret_token":"abcDEF123456"}"#,
            "abcDEF123456",
        ),
        (
            "card number as a JSON number",
            r#"{"n":4111111111111111}"#,
            "4111111111111111",
        ),
        (
            "card number in an array",
            "[4111111111111111,true,null]",
            "4111111111111111",
        ),
    ] {
        let mut r = redact::embedded().unwrap();
        r.add_rule("gated", r#"secret_token"?\s*[:=]\s*"?[A-Za-z0-9]{12}"#, "")
            .unwrap();
        let got = redacted_args(r, args);
        assert!(!got.contains(secret), "{name}: {got}");
        let v: Value = serde_json::from_str(&got).unwrap_or_else(|e| panic!("{name}: {got}: {e}"));
        assert!(!texts(&v).contains(secret), "{name}: {got}");
    }
}

/// A member flagged in key context loses its value, not the whole
/// arguments: the rest stays readable.
#[test]
fn arguments_key_context_keeps_the_rest() {
    let mut r = Redactor::new();
    r.add_rule("gated", r#"secret_token"?\s*[:=]\s*"?[A-Za-z0-9]{12}"#, "")
        .unwrap();
    let got = redacted_args(r, r#"{"secret_token":"abcDEF123456","city":"NYC"}"#);
    assert_eq!(
        serde_json::from_str::<Value>(&got).unwrap(),
        serde_json::json!({"secret_token": "REDACTED", "city": "NYC"}),
        "{got}"
    );
}

/// A number past what u64 and f64 hold is matched on the digits it was
/// sent with, not on a rounded copy.
#[test]
fn arguments_match_long_numbers_on_their_text() {
    let mut r = Redactor::new();
    r.add_rule("long", "12345678901234567890123", "").unwrap();
    let got = redacted_args(
        r,
        r#"{"n":12345678901234567890123,"m":[12345678901234567890123]}"#,
    );
    assert!(!got.contains("1234567890"), "{got}");
    assert!(!got.contains("e22"), "{got}");
    assert_eq!(
        serde_json::from_str::<Value>(&got).unwrap(),
        serde_json::json!({"n": "***REDACTED***", "m": ["***REDACTED***"]})
    );
}

/// Booleans, nulls and harmless numbers pass as they are, number text
/// included.
#[test]
fn arguments_keep_plain_values() {
    let args = r#"{"ok":true,"none":null,"n":3,"f":1.50,"e":2E+3,"list":[1,"a",false]}"#;
    let got = redacted_args(redact::embedded().unwrap(), args);
    let want: Value = serde_json::from_str(args).unwrap();
    assert_eq!(serde_json::from_str::<Value>(&got).unwrap(), want);
    assert!(got.contains("1.50") && got.contains("2E+3"), "{got}");
}

/// When the walk cannot clear the arguments (a rule spanning members),
/// or they do not decode, they become the fixed placeholder: nothing is
/// derived from the raw text.
#[test]
fn arguments_fallback_is_placeholder() {
    for (name, args) in [
        ("rule spans members", r#"{"user":"bob","pin":"1234"}"#),
        ("undecodable with a secret", r#"{"pw": hunter2"#),
        ("undecodable escaped secret", r#"{"pw":"hunter2""#),
        ("trailing data", r#"{"pw":"x"} hunter2"#),
        (
            "too deep",
            &format!("{}1{}", "[".repeat(200), "]".repeat(200)),
        ),
    ] {
        let mut r = Redactor::new();
        r.add_rule("span", r#""pin":"\d+","user":"bob""#, "")
            .unwrap();
        r.add_rule("pw", "hunter2", "").unwrap();
        assert_eq!(redacted_args(r, args), r#""REDACTED""#, "{name}");
    }
}

/// Serves `call_with_tools` with a fixed turn.
struct Turn(ToolResponse);

impl Provider for Turn {
    fn capabilities(&self) -> &'static [Capability] {
        &[Capability::ToolCall]
    }
    fn call_with_tools<'a>(
        &'a self,
        _req: &'a Request,
        _tools: &'a [ToolDef],
    ) -> BoxFuture<'a, Result<ToolResponse, LlmError>> {
        let r = self.0.clone();
        Box::pin(async move { Ok(r) })
    }
}

#[test]
fn redact_request_and_response_on_the_bus() {
    let mut r = Redactor::new();
    r.add_rule("pw", "hunter2", "[REDACTED]").unwrap();
    let r = Arc::new(r);

    let bus = DynBus::builder().build();
    let seen: Arc<Mutex<Vec<Value>>> = Arc::default();
    let s = seen.clone();
    bus.subscribe("kit.ai.#", move |e| {
        s.lock().unwrap().push(e.payload.clone());
        Ok(())
    });

    let req = Request {
        messages: vec![
            Message::system("be brief"),
            Message::user("my password is hunter2"),
            Message {
                thinking: vec![ThinkingBlock {
                    thinking: "hunter2?".into(),
                    signature: "sig".into(),
                    redacted: String::new(),
                }],
                ..Message::assistant_calls(
                    "",
                    vec![ToolCall {
                        id: "c1".into(),
                        name: "login".into(),
                        arguments: r#"{"pw":"hunter2"}"#.into(),
                        ..ToolCall::default()
                    }],
                )
            },
            Message {
                parts: vec![
                    Part::Text("again hunter2".into()),
                    Part::Media(Media {
                        kind: MediaKind::Image,
                        mime_type: "image/png".into(),
                        data: b"png".to_vec(),
                    }),
                ],
                ..Message::user("")
            },
        ],
        extensions: BTreeMap::from([("k".to_string(), Value::from("hunter2"))]),
        ..Request::default()
    };
    let turn = ToolResponse {
        content: "ok hunter2".into(),
        tool_calls: vec![ToolCall {
            id: "c2".into(),
            name: "login".into(),
            arguments: r#"{"pw":"hunter2"}"#.into(),
            ..ToolCall::default()
        }],
        ..ToolResponse::default()
    };
    let client = Client::new(Box::new(Turn(turn)))
        .with_bus(bus)
        .with_request_content(redact_request(r.clone()))
        .with_response_content(redact_response(r));
    tokio::runtime::Builder::new_current_thread()
        .build()
        .unwrap()
        .block_on(client.call_with_tools(&req, &[ToolDef::named("login")]))
        .unwrap();

    let seen = seen.lock().unwrap();
    assert_eq!(seen.len(), 2);
    let (started, ended) = (&seen[0], &seen[1]);
    for p in [started, ended] {
        assert!(!p.to_string().contains("hunter2"), "{p}");
        assert!(p.to_string().contains("REDACTED"), "{p}");
    }
    let content = &started["request"];
    assert!(
        content.get("extensions").is_none(),
        "extensions are not published"
    );
    assert_eq!(content["messages"][2]["thinking"][0]["signature"], "");
    let args = content["messages"][2]["tool_calls"][0]["arguments"]
        .as_str()
        .unwrap();
    serde_json::from_str::<Value>(args).expect("masked arguments stay JSON");
    assert_eq!(started["tools"], 1);
    assert_eq!(started["op"], "tool_call");
    assert_eq!(started["parts"], serde_json::json!({"text": 1, "image": 1}));
    assert!(
        req.messages[1].content.contains("hunter2"),
        "the caller's request is untouched"
    );
    assert_eq!(ended["tool_calls"], 1);
    assert!(ended["response"]["content"]
        .as_str()
        .unwrap()
        .contains("REDACTED"));
}

/// Media bytes never survive the request transform.
#[test]
fn redact_request_drops_media_bytes() {
    let req = Request {
        messages: vec![Message {
            parts: vec![Part::Media(Media {
                kind: MediaKind::Image,
                mime_type: "image/png".into(),
                data: b"png".to_vec(),
            })],
            ..Message::user("")
        }],
        ..Request::default()
    };
    let out = redact_request(Redactor::new())(req);
    let Part::Media(m) = &out.messages[0].parts[0] else {
        panic!("media part kept");
    };
    assert!(m.data.is_empty());
    assert_eq!(m.mime_type, "image/png");
}
