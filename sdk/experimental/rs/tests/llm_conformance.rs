//! Runs contracts/llm-v1. The vectors are the source of truth: a failure
//! means this port drifted from Go, not that a vector should be edited.
//! The provider-neutral sections need `llm`; the anthropic_* sections
//! need `llm-anthropic` and run against a loopback server.

#![cfg(feature = "llm")]

use std::collections::BTreeMap;
use std::path::PathBuf;

use hop_top_kit::llm::{parse_uri, redact_uri};
use serde::Deserialize;
use serde_json::Value;

#[cfg(feature = "llm-anthropic")]
mod llm_support;

fn corpus_dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../contracts/llm-v1")
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Corpus {
    version: String,
    description: String,
    redact_uri: Vec<RedactCase>,
    parse_uri: Vec<ParseCase>,
    #[cfg_attr(not(feature = "llm-anthropic"), allow(dead_code))]
    anthropic_request: Vec<RequestCase>,
    #[cfg_attr(not(feature = "llm-anthropic"), allow(dead_code))]
    anthropic_request_error: Vec<RequestErrorCase>,
    #[cfg_attr(not(feature = "llm-anthropic"), allow(dead_code))]
    anthropic_headers: Vec<HeaderCase>,
    #[cfg_attr(not(feature = "llm-anthropic"), allow(dead_code))]
    anthropic_stream: Vec<StreamCase>,
    #[cfg_attr(not(feature = "llm-anthropic"), allow(dead_code))]
    anthropic_accumulate: Vec<AccumulateCase>,
    #[cfg_attr(not(feature = "llm-anthropic"), allow(dead_code))]
    anthropic_error: Vec<ErrorCase>,
    #[cfg_attr(not(feature = "llm-anthropic"), allow(dead_code))]
    key_endpoint: Vec<KeyEndpointCase>,
    #[cfg_attr(not(feature = "llm-anthropic"), allow(dead_code))]
    credential_headers: Vec<CredentialCase>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct CredentialCase {
    name: String,
    base_url: String,
    yaml: String,
    env: BTreeMap<String, String>,
    sent: Vec<String>,
    withheld: Vec<String>,
    refused: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct KeyEndpointCase {
    name: String,
    op: String,
    uri: String,
    yaml: String,
    env: BTreeMap<String, String>,
    want_uri: String,
    refused: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RedactCase {
    name: String,
    #[serde(rename = "in")]
    input: String,
    want: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ParseCase {
    name: String,
    uri: String,
    #[serde(default)]
    scheme: String,
    #[serde(default)]
    model: String,
    #[serde(default)]
    host: String,
    #[serde(default)]
    params: Option<BTreeMap<String, String>>,
    error: String,
}

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct ConfPart {
    #[serde(rename = "type")]
    kind: String,
    #[serde(default)]
    text: String,
    #[serde(default)]
    mime_type: String,
    #[serde(default)]
    data: String,
}

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct ConfToolCall {
    #[serde(default)]
    id: String,
    name: String,
    #[serde(default)]
    arguments: String,
}

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct ConfMessage {
    role: String,
    #[serde(default)]
    content: String,
    #[serde(default)]
    parts: Vec<ConfPart>,
    #[serde(default)]
    tool_calls: Vec<ConfToolCall>,
    #[serde(default)]
    tool_call_id: String,
}

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct ConfRequest {
    #[serde(default)]
    model: String,
    #[serde(default)]
    temperature: Option<f64>,
    #[serde(default)]
    max_tokens: u32,
    #[serde(default)]
    stop_sequences: Vec<String>,
    messages: Vec<ConfMessage>,
}

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct ConfTool {
    name: String,
    #[serde(default)]
    description: String,
    #[serde(default)]
    parameters: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct RequestCase {
    name: String,
    adapter_model: String,
    request: ConfRequest,
    #[serde(default)]
    tools: Option<Vec<ConfTool>>,
    want_body: Value,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct RequestErrorCase {
    name: String,
    request: ConfRequest,
    error: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct HeaderCase {
    name: String,
    api_key: String,
    env: BTreeMap<String, String>,
    want: BTreeMap<String, String>,
    absent: Vec<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct StreamCase {
    name: String,
    sse: String,
    want_tokens: Vec<String>,
    want_error: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct WantCall {
    id: String,
    name: String,
    input: Value,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct WantUsage {
    input_tokens: u64,
    output_tokens: u64,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct WantAccumulated {
    #[serde(default)]
    text: String,
    #[serde(default)]
    thinking: String,
    #[serde(default)]
    tool_calls: Vec<WantCall>,
    #[serde(default)]
    stop_reason: String,
    #[serde(default)]
    usage: Option<WantUsage>,
    error: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct AccumulateCase {
    name: String,
    #[serde(default)]
    sse: String,
    #[serde(default)]
    sse_from: String,
    want: WantAccumulated,
}

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct ConfResponse {
    status: u16,
    headers: BTreeMap<String, String>,
    body: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct ErrorCase {
    name: String,
    responses: Vec<ConfResponse>,
    want_kind: String,
    want_status: u16,
    fallbackable: bool,
    attempts: usize,
}

fn load() -> Corpus {
    for entry in std::fs::read_dir(corpus_dir()).unwrap() {
        let name = entry.unwrap().file_name().into_string().unwrap();
        assert!(
            name == "vectors.json" || name == "README.md",
            "contracts/llm-v1/{name}: no runner consumes it"
        );
    }
    let raw = std::fs::read_to_string(corpus_dir().join("vectors.json")).unwrap();
    let c: Corpus = serde_json::from_str(&raw).unwrap();
    assert_eq!(c.version, "v1");
    assert!(!c.description.is_empty());
    c
}

#[test]
fn redact_uri_vectors() {
    let c = load();
    assert!(!c.redact_uri.is_empty());
    for v in &c.redact_uri {
        assert_eq!(redact_uri(&v.input), v.want, "{}", v.name);
    }
}

#[test]
fn parse_uri_vectors() {
    let c = load();
    assert!(!c.parse_uri.is_empty());
    for v in &c.parse_uri {
        match parse_uri(&v.uri) {
            Err(e) => assert_eq!(e.to_string(), v.error, "{}", v.name),
            Ok(u) => {
                assert!(v.error.is_empty(), "{}: want error {:?}", v.name, v.error);
                assert_eq!(u.scheme, v.scheme, "{}", v.name);
                assert_eq!(u.model, v.model, "{}", v.name);
                assert_eq!(u.host, v.host, "{}", v.name);
                let want = v.params.clone().unwrap_or_default();
                assert_eq!(u.params, want, "{}", v.name);
            }
        }
    }
}

#[cfg(feature = "llm-anthropic")]
mod anthropic {
    use super::*;
    use base64::Engine as _;
    use hop_top_kit::llm::anthropic::Adapter;
    use hop_top_kit::llm::{
        LlmError, Media, MediaKind, Message, Part, Provider, ProviderConfig, Request,
        ResolvedConfig, Role, ToolCall, ToolDef,
    };
    use llm_support::{serve, Canned};

    fn role(s: &str) -> Role {
        match s {
            "system" => Role::System,
            "user" => Role::User,
            "assistant" => Role::Assistant,
            "tool" => Role::Tool,
            other => panic!("unknown role {other}"),
        }
    }

    fn to_request(r: &ConfRequest) -> Request {
        let messages = r
            .messages
            .iter()
            .map(|m| Message {
                role: role(&m.role),
                content: m.content.clone(),
                parts: m
                    .parts
                    .iter()
                    .map(|p| match p.kind.as_str() {
                        "text" => Part::Text(p.text.clone()),
                        "image" => Part::Media(Media {
                            kind: MediaKind::Image,
                            mime_type: p.mime_type.clone(),
                            data: base64::engine::general_purpose::STANDARD
                                .decode(&p.data)
                                .unwrap(),
                        }),
                        other => panic!("unknown part {other}"),
                    })
                    .collect(),
                tool_calls: m
                    .tool_calls
                    .iter()
                    .map(|c| ToolCall {
                        id: c.id.clone(),
                        name: c.name.clone(),
                        arguments: c.arguments.clone(),
                        ..ToolCall::default()
                    })
                    .collect(),
                tool_call_id: m.tool_call_id.clone(),
            })
            .collect();
        Request {
            messages,
            model: r.model.clone(),
            temperature: r.temperature,
            max_tokens: r.max_tokens,
            stop_sequences: r.stop_sequences.clone(),
        }
    }

    fn to_tools(tools: &[ConfTool]) -> Vec<ToolDef> {
        tools
            .iter()
            .map(|t| ToolDef {
                name: t.name.clone(),
                description: t.description.clone(),
                parameters: (!t.parameters.is_empty())
                    .then(|| serde_json::from_str(&t.parameters).unwrap()),
            })
            .collect()
    }

    fn cfg(base: &str, model: &str, key: &str) -> ResolvedConfig {
        ResolvedConfig {
            provider: ProviderConfig {
                api_key: key.into(),
                base_url: base.into(),
                model: model.into(),
                ..ProviderConfig::default()
            },
            ..ResolvedConfig::default()
        }
    }

    fn adapter(base: &str, model: &str, key: &str) -> Adapter {
        adapter_with_env(base, model, key, BTreeMap::new())
    }

    fn adapter_with_env(
        base: &str,
        model: &str,
        key: &str,
        env: BTreeMap<String, String>,
    ) -> Adapter {
        Adapter::builder(cfg(base, model, key))
            .env(move |k| env.get(k).cloned())
            .build()
            .unwrap()
    }

    fn ok_message() -> Canned {
        Canned::json(
            200,
            r#"{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}"#,
        )
    }

    fn hi() -> Request {
        Request {
            messages: vec![Message::user("Hi")],
            ..Request::default()
        }
    }

    #[tokio::test]
    async fn request_vectors() {
        let c = load();
        assert!(!c.anthropic_request.is_empty());
        for v in &c.anthropic_request {
            let srv = serve(vec![ok_message()]).await;
            let a = adapter(&srv.url, &v.adapter_model, "k");
            let req = to_request(&v.request);
            match &v.tools {
                Some(tools) => {
                    a.call_with_tools(&req, &to_tools(tools)).await.unwrap();
                }
                None => {
                    a.complete(&req).await.unwrap();
                }
            }
            let got = srv.only();
            assert_eq!(got.path, "/v1/messages", "{}", v.name);
            assert_eq!(
                numeric(got.json()),
                numeric(v.want_body.clone()),
                "{}",
                v.name
            );
        }
    }

    #[tokio::test]
    async fn request_error_vectors() {
        let c = load();
        assert!(!c.anthropic_request_error.is_empty());
        for v in &c.anthropic_request_error {
            let srv = serve(vec![ok_message()]).await;
            let a = adapter(&srv.url, "m", "k");
            let err = a.complete(&to_request(&v.request)).await.unwrap_err();
            assert_eq!(err.to_string(), v.error, "{}", v.name);
            assert_eq!(
                srv.attempts(),
                0,
                "{}: a rejected request is never sent",
                v.name
            );
        }
    }

    #[tokio::test]
    async fn header_vectors() {
        let c = load();
        assert!(!c.anthropic_headers.is_empty());
        for v in &c.anthropic_headers {
            let srv = serve(vec![ok_message()]).await;
            let a = adapter_with_env(&srv.url, "m", &v.api_key, v.env.clone());
            a.complete(&hi()).await.unwrap();
            let got = srv.only();
            for (k, want) in &v.want {
                assert_eq!(got.header(k), Some(want.as_str()), "{}: {k}", v.name);
            }
            for k in &v.absent {
                assert_eq!(got.header(k), None, "{}: {k}", v.name);
            }
        }
    }

    #[tokio::test]
    async fn stream_vectors() {
        let c = load();
        assert!(!c.anthropic_stream.is_empty());
        for v in &c.anthropic_stream {
            let srv = serve(vec![Canned::sse(&v.sse)]).await;
            let a = adapter(&srv.url, "m", "k");
            let mut s = a.stream(&hi()).await.unwrap();
            let mut tokens = Vec::new();
            let mut stream_err: Option<LlmError> = None;
            loop {
                match s.next().await {
                    Ok(Some(t)) if t.done => {
                        assert!(
                            matches!(s.next().await, Ok(None)),
                            "{}: next after done is end",
                            v.name
                        );
                        break;
                    }
                    Ok(Some(t)) => tokens.push(t.content),
                    Ok(None) => break,
                    Err(e) => {
                        stream_err = Some(e);
                        break;
                    }
                }
            }
            assert_eq!(tokens, v.want_tokens, "{}", v.name);
            assert_eq!(
                stream_err.is_some(),
                v.want_error,
                "{}: {stream_err:?}",
                v.name
            );
            let body = srv.only().json();
            assert_eq!(body["stream"], Value::Bool(true), "{}", v.name);
        }
    }

    #[tokio::test]
    async fn accumulate_vectors() {
        let c = load();
        assert!(!c.anthropic_accumulate.is_empty());
        let streams: BTreeMap<String, String> = c
            .anthropic_stream
            .iter()
            .map(|s| (format!("anthropic_stream/{}", s.name), s.sse.clone()))
            .collect();
        for v in &c.anthropic_accumulate {
            let sse = if v.sse_from.is_empty() {
                v.sse.clone()
            } else {
                streams
                    .get(&v.sse_from)
                    .unwrap_or_else(|| panic!("sse_from {}", v.sse_from))
                    .clone()
            };
            let srv = serve(vec![Canned::sse(&sse)]).await;
            let a = adapter(&srv.url, "m", "k");
            let mut s = a
                .stream_with_tools(&hi(), &[ToolDef::named("get_weather")])
                .await
                .unwrap();
            let mut failed = false;
            loop {
                match s.next().await {
                    Ok(Some(t)) if t.done => break,
                    Ok(Some(_)) => {}
                    Ok(None) => break,
                    Err(_) => {
                        failed = true;
                        break;
                    }
                }
            }
            assert_eq!(failed, v.want.error, "{}", v.name);
            if v.want.error {
                continue;
            }
            let out = s.outcome().unwrap_or_else(|| panic!("{}: outcome", v.name));
            assert_eq!(out.content, v.want.text, "{}", v.name);
            assert_eq!(out.thinking, v.want.thinking, "{}", v.name);
            assert_eq!(out.finish_reason, v.want.stop_reason, "{}", v.name);
            let usage = v.want.usage.as_ref().unwrap();
            assert_eq!(out.usage.prompt_tokens, usage.input_tokens, "{}", v.name);
            assert_eq!(
                out.usage.completion_tokens, usage.output_tokens,
                "{}",
                v.name
            );
            assert_eq!(out.tool_calls.len(), v.want.tool_calls.len(), "{}", v.name);
            for (got, want) in out.tool_calls.iter().zip(&v.want.tool_calls) {
                assert_eq!(got.id, want.id, "{}", v.name);
                assert_eq!(got.name, want.name, "{}", v.name);
                let input: Value = serde_json::from_str(&got.arguments).unwrap();
                assert_eq!(input, want.input, "{}", v.name);
            }
        }
    }

    /// JSON compared as Go's `JSONEq` does: every number as an f64, so
    /// `0` and `0.0` are equal.
    fn numeric(v: Value) -> Value {
        match v {
            Value::Number(n) => serde_json::json!(n.as_f64().unwrap()),
            Value::Array(a) => Value::Array(a.into_iter().map(numeric).collect()),
            Value::Object(o) => {
                Value::Object(o.into_iter().map(|(k, v)| (k, numeric(v))).collect())
            }
            other => other,
        }
    }

    fn kind(e: &LlmError) -> (&'static str, u16) {
        match e {
            LlmError::Auth { .. } => ("auth", 0),
            LlmError::RateLimit { .. } => ("rate_limit", 0),
            LlmError::Model { .. } => ("model", 0),
            LlmError::HttpStatus { status, .. } => ("http_status", *status),
            LlmError::Api { status, .. } => ("api", *status),
            _ => ("other", 0),
        }
    }

    #[tokio::test]
    async fn error_vectors() {
        let c = load();
        assert!(!c.anthropic_error.is_empty());
        for v in &c.anthropic_error {
            let canned = v
                .responses
                .iter()
                .map(|r| {
                    let mut headers: Vec<(String, String)> = r
                        .headers
                        .iter()
                        .map(|(k, v)| (k.clone(), v.clone()))
                        .collect();
                    if !r.headers.contains_key("content-type") {
                        headers.push(("content-type".into(), "application/json".into()));
                    }
                    Canned {
                        status: r.status,
                        headers,
                        body: r.body.clone(),
                        hang: false,
                    }
                })
                .collect();
            let srv = serve(canned).await;
            let a = adapter(&srv.url, "m", "k");
            // A wait the client should not honor fails here, not hangs.
            let res = tokio::time::timeout(std::time::Duration::from_secs(10), a.complete(&hi()))
                .await
                .unwrap_or_else(|_| panic!("{}: timed out", v.name));
            let (k, status) = match &res {
                Ok(_) => ("", 0),
                Err(e) => kind(e),
            };
            assert_eq!(k, v.want_kind, "{}: {res:?}", v.name);
            assert_eq!(status, v.want_status, "{}", v.name);
            let fallbackable = res.as_ref().err().is_some_and(LlmError::is_fallbackable);
            assert_eq!(fallbackable, v.fallbackable, "{}", v.name);
            assert_eq!(srv.attempts(), v.attempts, "{}", v.name);
        }
    }

    #[test]
    fn key_endpoint_vectors() {
        use hop_top_kit::llm::{default_registry, ConfigFile, Sources};
        let c = load();
        assert!(!c.key_endpoint.is_empty());
        for v in &c.key_endpoint {
            let file = ConfigFile::parse(&v.yaml).unwrap();
            let vars = v.env.clone();
            let env = move |k: &str| vars.get(k).cloned();
            let src = Sources {
                file: &file,
                env: &env,
                store: None,
            };
            let r = default_registry();
            let res: Result<(), LlmError> = match v.op.as_str() {
                "apply" => r.apply_api_key(&src, &v.uri).map(|got| {
                    assert_eq!(got, v.want_uri, "{}", v.name);
                }),
                "load" => r.load_config(&src, &v.uri).map(|_| ()),
                "resolve" => r.resolve(&v.uri).map(|_| ()),
                op => panic!("unknown op {op}"),
            };
            match res {
                Err(LlmError::KeyEndpoint { .. }) => assert!(v.refused, "{}: refused", v.name),
                Err(e) => panic!("{}: {e}", v.name),
                Ok(()) => assert!(!v.refused, "{}: not refused", v.name),
            }
        }
    }

    #[test]
    fn credential_header_vectors() {
        use hop_top_kit::llm::ConfigFile;
        let c = load();
        assert!(!c.credential_headers.is_empty());
        for v in &c.credential_headers {
            let vars = v.env.clone();
            let built = Adapter::builder(cfg(&v.base_url, "m", "kit-key"))
                .env(move |k| vars.get(k).cloned())
                .config_file(ConfigFile::parse(&v.yaml).unwrap())
                .build();
            if v.refused {
                assert!(
                    matches!(built, Err(LlmError::KeyEndpoint { .. })),
                    "{}: refused",
                    v.name
                );
                continue;
            }
            let names = built.unwrap().credential_headers();
            for h in &v.sent {
                assert!(names.contains(&h.as_str()), "{}: {h} sent", v.name);
            }
            for h in &v.withheld {
                assert!(!names.contains(&h.as_str()), "{}: {h} withheld", v.name);
            }
        }
    }
}
