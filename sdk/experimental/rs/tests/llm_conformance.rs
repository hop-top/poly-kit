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
    #[cfg_attr(not(feature = "llm-anthropic"), allow(dead_code))]
    anthropic_limits: Vec<LimitCase>,
    #[cfg_attr(not(feature = "llm-anthropic"), allow(dead_code))]
    anthropic_response: Vec<ResponseCase>,
    usage_cost: Vec<CostCase>,
    tool_names: Vec<ToolNamesCase>,
    #[cfg_attr(not(feature = "llm-anthropic"), allow(dead_code))]
    anthropic_tool_names: Vec<AnthropicToolNamesCase>,
    // Sections below are pending Rust port: decoded loosely, not run.
    #[cfg_attr(not(feature = "llm-anthropic"), allow(dead_code))]
    anthropic_tool_choice: Vec<ToolChoiceCase>,
    #[allow(dead_code)]
    anthropic_hermetic: Vec<Value>,
    #[allow(dead_code)]
    anthropic_stream_events: Vec<Value>,
    #[allow(dead_code)]
    bus_request_started: Vec<Value>,
    #[allow(dead_code)]
    bus_response_received: Vec<Value>,
    /// URL media fetch guard. Not applicable: this port has no URL media
    /// sources, so nothing is fetched (README, Known differences).
    media_fetch: Vec<Value>,
    /// Gemini stream events. Not applicable: this port has no gemini
    /// adapter (README, Known differences).
    gemini_stream_events: Vec<Value>,
    // openai_* sections. Not applicable: this port has no openai
    // adapter (README, Runners); checked present only.
    openai_request: Vec<Value>,
    openai_stream: Vec<Value>,
    openai_stream_events: Vec<Value>,
    openai_accumulate: Vec<Value>,
    openai_error: Vec<Value>,
    openai_limits: Vec<Value>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[cfg_attr(not(feature = "llm-anthropic"), allow(dead_code))]
struct ConfToolChoice {
    mode: String,
    #[serde(default)]
    name: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[cfg_attr(not(feature = "llm-anthropic"), allow(dead_code))]
struct ToolChoiceCase {
    name: String,
    call: String,
    tools: Vec<String>,
    tool_choice: Option<ConfToolChoice>,
    #[serde(default)]
    want_tool_choice: Option<Value>,
    #[serde(default)]
    error: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ToolNameErr {
    reason: String,
    name: String,
    other: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ToolNamesCase {
    name: String,
    rule: String,
    #[serde(default)]
    mode: String,
    tools: Vec<String>,
    #[serde(default)]
    want: Vec<String>,
    #[serde(default)]
    calls: Vec<String>,
    #[serde(default)]
    want_calls: Vec<String>,
    #[serde(default)]
    error: Option<ToolNameErr>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct NamedCall {
    id: String,
    name: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[cfg_attr(not(feature = "llm-anthropic"), allow(dead_code))]
struct AnthropicToolNamesCase {
    name: String,
    call: String,
    #[serde(default)]
    params: BTreeMap<String, String>,
    #[serde(default)]
    messages: Option<Vec<ConfMessage>>,
    #[serde(default)]
    tools: Vec<String>,
    #[serde(default)]
    body: String,
    #[serde(default)]
    want_tools: Vec<String>,
    #[serde(default)]
    want_history: Vec<String>,
    #[serde(default)]
    want_calls: Vec<NamedCall>,
    #[serde(default)]
    error: Option<ToolNameErr>,
    #[serde(default)]
    refused: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct ConfUsage {
    prompt_tokens: u64,
    completion_tokens: u64,
    cache_read_tokens: u64,
    cache_creation_tokens: u64,
    #[serde(default)]
    total_tokens: u64,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct WantResponse {
    content: String,
    thinking_blocks: Vec<ConfThought>,
    tool_calls: Vec<WantCall>,
    stop_reason: String,
    usage: ConfUsage,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct ResponseCase {
    name: String,
    tools: bool,
    body: String,
    want: WantResponse,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ConfPrice {
    input: f64,
    output: f64,
    cache_read: f64,
    cache_write: f64,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CostCase {
    name: String,
    usage: ConfUsage,
    price: ConfPrice,
    want: f64,
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
    /// A URL media part. This port has no URL media sources: vectors
    /// carrying one are skipped by name (README, Known differences).
    #[serde(default)]
    url: String,
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
    #[serde(default)]
    cache_control: Option<ConfCache>,
    #[serde(default)]
    thinking: Vec<ConfThought>,
}

#[derive(Deserialize, Clone, Debug, PartialEq)]
#[serde(deny_unknown_fields)]
struct ConfCache {
    ttl: String,
}

#[derive(Deserialize, Clone, Debug, PartialEq)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct ConfThought {
    #[serde(default)]
    thinking: String,
    #[serde(default)]
    signature: String,
    #[serde(default)]
    redacted: String,
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
    #[serde(default)]
    thinking_budget: u32,
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
    #[serde(default)]
    cache_control: Option<ConfCache>,
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
    error_kind: String,
    thinking_budget: u32,
    want_thinking: Vec<String>,
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
    #[serde(default)]
    cache_read_tokens: u64,
    #[serde(default)]
    cache_creation_tokens: u64,
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
    #[serde(default)]
    thinking_blocks: Option<Vec<ConfThought>>,
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
    stream: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct LimitCase {
    name: String,
    params: BTreeMap<String, String>,
    stream: bool,
    content_type: String,
    body: String,
    too_large: bool,
}

fn load() -> Corpus {
    for entry in std::fs::read_dir(corpus_dir()).unwrap() {
        let name = entry.unwrap().file_name().into_string().unwrap();
        assert!(
            name == "vectors.json" || name == "README.md" || name == "tool-names.md",
            "contracts/llm-v1/{name}: no runner consumes it"
        );
    }
    let raw = std::fs::read_to_string(corpus_dir().join("vectors.json")).unwrap();
    let c: Corpus = serde_json::from_str(&raw).unwrap();
    assert_eq!(c.version, "v1");
    assert!(!c.description.is_empty());
    c
}

/// The URL media source and its fetch guard are not ported: Rust media
/// are bytes the caller has read, so no URL is ever fetched. The section
/// must still be present and well formed.
#[test]
fn media_fetch_vectors_not_applicable() {
    let c = load();
    assert!(!c.media_fetch.is_empty());
    for v in &c.media_fetch {
        let name = v.get("name").and_then(Value::as_str).unwrap_or_default();
        assert!(!name.is_empty(), "media_fetch vector without a name");
        eprintln!("media_fetch/{name}: not applicable (no URL media sources in this port)");
    }
}

/// Gemini stream-event vectors pin the gemini adapter, which this port
/// does not have; the section must still be present and well formed.
#[test]
fn gemini_stream_events_vectors_not_applicable() {
    let c = load();
    assert!(!c.gemini_stream_events.is_empty());
    for v in &c.gemini_stream_events {
        let name = v.get("name").and_then(Value::as_str).unwrap_or_default();
        assert!(
            !name.is_empty(),
            "gemini_stream_events vector without a name"
        );
        eprintln!("gemini_stream_events/{name}: not applicable (no gemini adapter in this port)");
    }
}

/// The openai_* sections pin the openai adapter, which this port does
/// not have. They must still be present and well formed.
#[test]
fn openai_vectors_not_applicable() {
    let c = load();
    for (section, vs) in [
        ("openai_request", &c.openai_request),
        ("openai_stream", &c.openai_stream),
        ("openai_stream_events", &c.openai_stream_events),
        ("openai_accumulate", &c.openai_accumulate),
        ("openai_error", &c.openai_error),
        ("openai_limits", &c.openai_limits),
    ] {
        assert!(!vs.is_empty(), "{section} is empty");
        eprintln!(
            "{section}: {} vectors not applicable (no openai adapter in this port)",
            vs.len()
        );
    }
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
fn tool_names_vectors() {
    use hop_top_kit::llm::{
        LlmError, ToolDef, ToolNameMode, ToolNames, ANTHROPIC_TOOL_NAMES, GEMINI_TOOL_NAMES,
        OPENAI_TOOL_NAMES,
    };
    let c = load();
    assert!(!c.tool_names.is_empty());
    for v in &c.tool_names {
        let rule = match v.rule.as_str() {
            "anthropic" => ANTHROPIC_TOOL_NAMES,
            "openai" => OPENAI_TOOL_NAMES,
            "gemini" => GEMINI_TOOL_NAMES,
            other => panic!("{}: unknown rule {other:?}", v.name),
        };
        let mode = match v.mode.as_str() {
            "" | "sanitize" => ToolNameMode::Sanitize,
            "strict" => ToolNameMode::Strict,
            other => panic!("{}: unknown mode {other:?}", v.name),
        };
        let defs: Vec<ToolDef> = v.tools.iter().map(ToolDef::named).collect();
        let res = ToolNames::new(rule, mode, &defs);
        if let Some(want) = &v.error {
            match res {
                Err(LlmError::ToolName {
                    provider,
                    reason,
                    name,
                    other,
                }) => {
                    assert_eq!(reason.as_str(), want.reason, "{}", v.name);
                    assert_eq!(name, want.name, "{}", v.name);
                    assert_eq!(other, want.other, "{}", v.name);
                    assert_eq!(provider, rule.provider, "{}", v.name);
                }
                Err(e) => panic!("{}: want a tool-name error, got {e:?}", v.name),
                Ok(_) => panic!("{}: want a tool-name error, got none", v.name),
            }
            continue;
        }
        let names = res.unwrap_or_else(|e| panic!("{}: {e}", v.name));
        let got: Vec<String> = v.tools.iter().map(|n| names.wire(n)).collect();
        assert_eq!(got, v.want, "{}", v.name);
        for w in &got {
            assert!(rule.valid(w), "{}: wire name {w:?} invalid", v.name);
        }
        if mode == ToolNameMode::Sanitize {
            for (n, want) in v.tools.iter().zip(&v.want) {
                assert_eq!(
                    &rule.sanitize(n).unwrap(),
                    want,
                    "{}: sanitize({n:?})",
                    v.name
                );
            }
        }
        let back: Vec<&str> = v.calls.iter().map(|c| names.original(c)).collect();
        assert_eq!(back, v.want_calls, "{}", v.name);
    }
}

#[test]
fn usage_cost_vectors() {
    use hop_top_kit::llm::{Price, Usage};
    let c = load();
    assert!(!c.usage_cost.is_empty());
    for v in &c.usage_cost {
        let u = Usage {
            prompt_tokens: v.usage.prompt_tokens,
            completion_tokens: v.usage.completion_tokens,
            cache_read_tokens: v.usage.cache_read_tokens,
            cache_creation_tokens: v.usage.cache_creation_tokens,
            total_tokens: v.usage.total_tokens,
        };
        let p = Price {
            input: v.price.input,
            output: v.price.output,
            cache_read: v.price.cache_read,
            cache_write: v.price.cache_write,
        };
        assert!(
            (u.cost(&p) - v.want).abs() < 1e-9,
            "{}: {}",
            v.name,
            u.cost(&p)
        );
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
        CacheControl, LlmError, Media, MediaKind, Message, Part, Provider, ProviderConfig, Request,
        ResolvedConfig, Response, Role, Thinking, ThinkingBlock, ToolCall, ToolDef,
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

    fn has_url_part(r: &ConfRequest) -> bool {
        r.messages
            .iter()
            .any(|m| m.parts.iter().any(|p| !p.url.is_empty()))
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
                cache_control: m.cache_control.as_ref().map(cache),
                thinking: thoughts(&m.thinking),
            })
            .collect();
        Request {
            messages,
            model: r.model.clone(),
            temperature: r.temperature,
            max_tokens: r.max_tokens,
            stop_sequences: r.stop_sequences.clone(),
            thinking: (r.thinking_budget > 0).then_some(Thinking {
                budget_tokens: r.thinking_budget,
            }),
            ..Request::default()
        }
    }

    fn cache(c: &ConfCache) -> CacheControl {
        CacheControl { ttl: c.ttl.clone() }
    }

    fn thoughts(ts: &[ConfThought]) -> Vec<ThinkingBlock> {
        ts.iter()
            .map(|t| ThinkingBlock {
                thinking: t.thinking.clone(),
                signature: t.signature.clone(),
                redacted: t.redacted.clone(),
            })
            .collect()
    }

    fn to_tools(tools: &[ConfTool]) -> Vec<ToolDef> {
        tools
            .iter()
            .map(|t| ToolDef {
                name: t.name.clone(),
                description: t.description.clone(),
                parameters: (!t.parameters.is_empty())
                    .then(|| serde_json::from_str(&t.parameters).unwrap()),
                cache_control: t.cache_control.as_ref().map(cache),
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
            if has_url_part(&v.request) {
                eprintln!("{}: skipped, no URL media sources in this port", v.name);
                continue;
            }
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
            let mut req = hi();
            if v.thinking_budget > 0 {
                req.max_tokens = 4 * v.thinking_budget;
                req.thinking = Some(Thinking {
                    budget_tokens: v.thinking_budget,
                });
            }
            let mut s = a.stream(&req).await.unwrap();
            let mut tokens = Vec::new();
            let mut thinking = Vec::new();
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
                    Ok(Some(t)) if !t.thinking.is_empty() => thinking.push(t.thinking),
                    Ok(Some(t)) => tokens.push(t.content),
                    Ok(None) => break,
                    Err(e) => {
                        stream_err = Some(e);
                        break;
                    }
                }
            }
            assert_eq!(tokens, v.want_tokens, "{}", v.name);
            assert_eq!(thinking, v.want_thinking, "{}", v.name);
            assert_eq!(
                stream_err.is_some(),
                v.want_error,
                "{}: {stream_err:?}",
                v.name
            );
            let k = stream_err.as_ref().map_or("", |e| kind(e).0);
            assert_eq!(k, v.error_kind, "{}: {stream_err:?}", v.name);
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
            let thought: String = out.thinking.iter().map(|b| b.thinking.as_str()).collect();
            assert_eq!(thought, v.want.thinking, "{}", v.name);
            if let Some(want) = &v.want.thinking_blocks {
                assert_eq!(out.thinking, thoughts(want), "{}", v.name);
            }
            assert_eq!(out.finish_reason, v.want.stop_reason, "{}", v.name);
            let usage = v.want.usage.as_ref().unwrap();
            assert_eq!(out.usage.prompt_tokens, usage.input_tokens, "{}", v.name);
            assert_eq!(
                out.usage.completion_tokens, usage.output_tokens,
                "{}",
                v.name
            );
            assert_eq!(
                out.usage.cache_read_tokens, usage.cache_read_tokens,
                "{}",
                v.name
            );
            assert_eq!(
                out.usage.cache_creation_tokens, usage.cache_creation_tokens,
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
            LlmError::Truncated { .. } => ("truncated", 0),
            // A permission denial is an auth error too (Go: errors.As
            // matches both); a kind that is not reports itself.
            LlmError::PermissionDenied { .. } if !e.is_auth() => {
                ("permission_denied without auth", 0)
            }
            LlmError::PermissionDenied { .. } => ("permission_denied", 0),
            LlmError::ResponseTooLarge { .. } => ("too_large", 0),
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
            let a = adapter(&srv.url, "m", CONF_KEY);
            // A wait the client should not honor fails here, not hangs.
            let call = async {
                if v.stream {
                    drain(&a).await.map(|_| Response::default())
                } else {
                    a.complete(&hi()).await
                }
            };
            let res = tokio::time::timeout(std::time::Duration::from_secs(10), call)
                .await
                .unwrap_or_else(|_| panic!("{}: timed out", v.name));
            if let Err(e) = &res {
                assert!(!e.to_string().contains(CONF_KEY), "{}: {e}", v.name);
                assert!(!format!("{e:?}").contains(CONF_KEY), "{}: {e:?}", v.name);
            }
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

    /// The key error vectors run with; no error text may hold it.
    const CONF_KEY: &str = "conf-key-must-not-leak";

    /// Open a stream and read it to its end.
    async fn drain(a: &Adapter) -> Result<(), LlmError> {
        let mut s = a.stream(&hi()).await?;
        loop {
            match s.next().await? {
                Some(t) if t.done => return Ok(()),
                Some(_) => {}
                None => return Ok(()),
            }
        }
    }

    #[tokio::test]
    async fn limit_vectors() {
        let c = load();
        assert!(!c.anthropic_limits.is_empty());
        for v in &c.anthropic_limits {
            let srv = serve(vec![Canned {
                status: 200,
                headers: vec![("content-type".into(), v.content_type.clone())],
                body: v.body.clone(),
                hang: false,
            }])
            .await;
            let mut rc = cfg(&srv.url, "m", "k");
            rc.provider.params = v.params.clone();
            let a = Adapter::builder(rc).env(|_| None).build().unwrap();
            let res = if v.stream {
                drain(&a).await
            } else {
                a.complete(&hi()).await.map(|_| ())
            };
            match res {
                Err(LlmError::ResponseTooLarge { .. }) => assert!(v.too_large, "{}", v.name),
                Err(e) => panic!("{}: {e}", v.name),
                Ok(()) => assert!(!v.too_large, "{}: not refused", v.name),
            }
        }
    }

    #[tokio::test]
    async fn response_vectors() {
        let c = load();
        assert!(!c.anthropic_response.is_empty());
        for v in &c.anthropic_response {
            let srv = serve(vec![Canned::json(200, &v.body)]).await;
            let a = adapter(&srv.url, "m", "k");
            let (content, stop, usage, thinking, calls) = if v.tools {
                let r = a
                    .call_with_tools(&hi(), &[ToolDef::named("f")])
                    .await
                    .unwrap();
                (
                    r.content,
                    r.finish_reason,
                    r.usage,
                    r.thinking,
                    r.tool_calls,
                )
            } else {
                let r = a.complete(&hi()).await.unwrap();
                (r.content, r.finish_reason, r.usage, r.thinking, Vec::new())
            };
            assert_eq!(content, v.want.content, "{}", v.name);
            assert_eq!(stop, v.want.stop_reason, "{}", v.name);
            let w = &v.want.usage;
            assert_eq!(
                (
                    usage.prompt_tokens,
                    usage.completion_tokens,
                    usage.cache_read_tokens,
                    usage.cache_creation_tokens,
                    usage.total_tokens
                ),
                (
                    w.prompt_tokens,
                    w.completion_tokens,
                    w.cache_read_tokens,
                    w.cache_creation_tokens,
                    w.total_tokens
                ),
                "{}",
                v.name
            );
            assert_eq!(thinking, thoughts(&v.want.thinking_blocks), "{}", v.name);
            assert_eq!(calls.len(), v.want.tool_calls.len(), "{}", v.name);
            for (got, want) in calls.iter().zip(&v.want.tool_calls) {
                assert_eq!(got.id, want.id, "{}", v.name);
                let input: Value = serde_json::from_str(&got.arguments).unwrap();
                assert_eq!(input, want.input, "{}", v.name);
            }
        }
    }

    /// The tool names and the history's tool_use names a request sent.
    fn sent_tool_names(body: &Value) -> (Vec<String>, Vec<String>) {
        let names = |v: &Value| v["name"].as_str().unwrap_or_default().to_string();
        let tools = body["tools"].as_array().into_iter().flatten().map(names);
        let history = body["messages"]
            .as_array()
            .into_iter()
            .flatten()
            .flat_map(|m| m["content"].as_array().into_iter().flatten())
            .filter(|c| c["type"] == "tool_use")
            .map(names);
        (tools.collect(), history.collect())
    }

    /// Read a stream to its done token; its outcome's calls.
    async fn drain_calls(
        mut s: Box<dyn hop_top_kit::llm::TokenStream>,
    ) -> Result<Vec<ToolCall>, LlmError> {
        while let Some(t) = s.next().await? {
            if t.done {
                break;
            }
        }
        Ok(s.outcome().expect("outcome after done").tool_calls.clone())
    }

    #[tokio::test]
    async fn tool_names_vectors() {
        let c = load();
        assert!(!c.anthropic_tool_names.is_empty());
        for v in &c.anthropic_tool_names {
            let streamed = v.call == "stream" || v.call == "stream_tools";
            let canned = if streamed {
                Canned::sse(&v.body)
            } else {
                Canned::json(200, &v.body)
            };
            let srv = serve(vec![canned]).await;
            let mut rc = cfg(&srv.url, "m", "k");
            rc.provider.params = v.params.clone();
            let built = Adapter::builder(rc).env(|_| None).build();
            if v.refused {
                assert!(built.is_err(), "{}: not refused", v.name);
                continue;
            }
            let a = built.unwrap_or_else(|e| panic!("{}: {e}", v.name));
            let req = match &v.messages {
                Some(msgs) => to_request(&ConfRequest {
                    model: String::new(),
                    temperature: None,
                    max_tokens: 0,
                    stop_sequences: Vec::new(),
                    messages: msgs.clone(),
                    thinking_budget: 0,
                }),
                None => hi(),
            };
            let defs: Vec<ToolDef> = v.tools.iter().map(ToolDef::named).collect();
            let res = match v.call.as_str() {
                "tools" => a.call_with_tools(&req, &defs).await.map(|r| r.tool_calls),
                "stream_tools" => match a.stream_with_tools(&req, &defs).await {
                    Ok(s) => drain_calls(s).await,
                    Err(e) => Err(e),
                },
                "complete" => a.complete(&req).await.map(|_| Vec::new()),
                "stream" => match a.stream(&req).await {
                    Ok(s) => drain_calls(s).await.map(|_| Vec::new()),
                    Err(e) => Err(e),
                },
                other => panic!("{}: unknown call {other:?}", v.name),
            };
            if let Some(want) = &v.error {
                let e = res.expect_err(&v.name);
                assert!(!e.is_fallbackable(), "{}", v.name);
                match e {
                    LlmError::ToolName {
                        reason,
                        name,
                        other,
                        ..
                    } => {
                        assert_eq!(reason.as_str(), want.reason, "{}", v.name);
                        assert_eq!(name, want.name, "{}", v.name);
                        assert_eq!(other, want.other, "{}", v.name);
                    }
                    e => panic!("{}: want a tool-name error, got {e:?}", v.name),
                }
                assert_eq!(
                    srv.attempts(),
                    0,
                    "{}: a rejected request reaches no server",
                    v.name
                );
                continue;
            }
            let calls = res.unwrap_or_else(|e| panic!("{}: {e}", v.name));
            let (tools, history) = sent_tool_names(&srv.only().json());
            assert_eq!(tools, v.want_tools, "{}", v.name);
            assert_eq!(history, v.want_history, "{}", v.name);
            assert_eq!(calls.len(), v.want_calls.len(), "{}", v.name);
            for (got, want) in calls.iter().zip(&v.want_calls) {
                assert_eq!(got.id, want.id, "{}", v.name);
                assert_eq!(got.name, want.name, "{}", v.name);
            }
        }
    }

    /// A complete, empty streamed turn.
    const MINIMAL_SSE: &str = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"m\",\"stop_reason\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n";

    #[tokio::test]
    async fn tool_choice_vectors() {
        use hop_top_kit::llm::{ToolChoice, ToolChoiceMode};
        let c = load();
        assert!(!c.anthropic_tool_choice.is_empty());
        for v in &c.anthropic_tool_choice {
            let streamed = v.call == "stream" || v.call == "stream_tools";
            let canned = if streamed {
                Canned::sse(MINIMAL_SSE)
            } else {
                ok_message()
            };
            let srv = serve(vec![canned]).await;
            let a = adapter(&srv.url, "m", "k");
            let mut req = hi();
            req.tool_choice = v.tool_choice.as_ref().map(|c| ToolChoice {
                mode: ToolChoiceMode::new(c.mode.clone()),
                name: c.name.clone(),
            });
            let defs: Vec<ToolDef> = v.tools.iter().map(ToolDef::named).collect();
            let res = match v.call.as_str() {
                "complete" => a.complete(&req).await.map(|_| ()),
                "stream" => a.stream(&req).await.map(|_| ()),
                "tools" => a.call_with_tools(&req, &defs).await.map(|_| ()),
                "stream_tools" => a.stream_with_tools(&req, &defs).await.map(|_| ()),
                other => panic!("{}: unknown call {other:?}", v.name),
            };
            if !v.error.is_empty() {
                let e = res.expect_err(&v.name);
                assert_eq!(e.to_string(), v.error, "{}", v.name);
                assert_eq!(
                    srv.attempts(),
                    0,
                    "{}: a rejected request is never sent",
                    v.name
                );
                continue;
            }
            res.unwrap_or_else(|e| panic!("{}: {e}", v.name));
            let body = srv.only().json();
            match &v.want_tool_choice {
                None | Some(Value::Null) => {
                    assert!(
                        body.get("tool_choice").is_none(),
                        "{}: no tool_choice sent",
                        v.name
                    )
                }
                Some(want) => assert_eq!(&body["tool_choice"], want, "{}", v.name),
            }
        }
    }
}
