//! Runs the openai_* sections of contracts/llm-v1 against the OpenAI
//! adapter, over a loopback server. The vectors are the source of truth:
//! a failure means this port drifted from Go, not that a vector should be
//! edited. The other sections run in llm_conformance.

#![cfg(feature = "llm-openai")]

mod llm_support;

use std::collections::BTreeMap;
use std::path::PathBuf;

use hop_top_kit::llm::openai::Adapter;
use hop_top_kit::llm::{
    LlmError, Message, Provider, ProviderConfig, Request, ResolvedConfig, ToolCall, ToolCallEvent,
    ToolCallEventKind, ToolChoice, ToolChoiceMode, ToolDef, Usage,
};
use llm_support::{serve, Canned};
use serde::Deserialize;
use serde_json::{json, Value};

/// The key the vectors run with; no error text may hold it.
const CONF_KEY: &str = "conf-key-must-not-leak";

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ConfToolChoice {
    mode: String,
    #[serde(default)]
    name: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ConfCall {
    id: String,
    name: String,
    arguments: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ConfMessage {
    role: String,
    #[serde(default)]
    content: String,
    #[serde(default)]
    tool_calls: Vec<ConfCall>,
    #[serde(default)]
    tool_call_id: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
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
    tool_choice: Option<ConfToolChoice>,
    #[serde(default)]
    stream_events: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ConfTool {
    name: String,
    description: String,
    parameters: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RequestCase {
    name: String,
    call: String,
    adapter_model: String,
    request: ConfRequest,
    #[serde(default)]
    tools: Vec<ConfTool>,
    want_body: Value,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct StreamCase {
    name: String,
    sse: String,
    want_tokens: Vec<String>,
    error_kind: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct EventsCase {
    name: String,
    #[serde(default)]
    sse: String,
    #[serde(default)]
    sse_from: String,
    tools: Vec<String>,
    stream_events: bool,
    want_events: Vec<Value>,
    error_kind: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WantCall {
    id: String,
    name: String,
    arguments: Value,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WantUsage {
    prompt_tokens: u64,
    completion_tokens: u64,
    total_tokens: u64,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Want {
    text: String,
    tool_calls: Vec<WantCall>,
    finish_reason: String,
    usage: WantUsage,
    error: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AccumulateCase {
    name: String,
    #[serde(default)]
    sse: String,
    #[serde(default)]
    sse_from: String,
    tools: Vec<String>,
    want: Want,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ConfResponse {
    status: u16,
    headers: BTreeMap<String, String>,
    body: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
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
struct LimitCase {
    name: String,
    params: BTreeMap<String, String>,
    stream: bool,
    content_type: String,
    body: String,
    #[serde(default)]
    too_large: bool,
}

/// Only the openai_* sections: the others belong to llm_conformance.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Corpus {
    openai_request: Vec<RequestCase>,
    openai_stream: Vec<StreamCase>,
    openai_stream_events: Vec<EventsCase>,
    openai_accumulate: Vec<AccumulateCase>,
    openai_error: Vec<ErrorCase>,
    openai_limits: Vec<LimitCase>,
}

fn load() -> Corpus {
    let path =
        PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../contracts/llm-v1/vectors.json");
    let raw: Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    assert_eq!(raw["version"], "v1");
    let only: serde_json::Map<String, Value> = raw
        .as_object()
        .unwrap()
        .iter()
        .filter(|(k, _)| k.starts_with("openai_"))
        .map(|(k, v)| (k.clone(), v.clone()))
        .collect();
    serde_json::from_value(Value::Object(only)).expect("openai_* sections decode strictly")
}

fn adapter(base: &str, model: &str, params: BTreeMap<String, String>) -> Adapter {
    Adapter::new(ResolvedConfig {
        provider: ProviderConfig {
            api_key: CONF_KEY.into(),
            base_url: base.into(),
            model: model.into(),
            params,
            ..ProviderConfig::default()
        },
        ..ResolvedConfig::default()
    })
    .unwrap()
}

fn hi() -> Request {
    Request {
        messages: vec![Message::user("Hi")],
        ..Request::default()
    }
}

fn to_request(r: &ConfRequest) -> Request {
    let mut out = Request {
        model: r.model.clone(),
        temperature: r.temperature,
        max_tokens: r.max_tokens,
        stop_sequences: r.stop_sequences.clone(),
        stream_events: r.stream_events,
        tool_choice: r.tool_choice.as_ref().map(|c| ToolChoice {
            mode: ToolChoiceMode::new(c.mode.clone()),
            name: c.name.clone(),
        }),
        ..Request::default()
    };
    for m in &r.messages {
        let mut msg = match m.role.as_str() {
            "system" => Message::system(&m.content),
            "assistant" => Message::assistant(&m.content),
            "tool" => Message::tool_result(&m.tool_call_id, &m.content),
            _ => Message::user(&m.content),
        };
        msg.tool_calls = m
            .tool_calls
            .iter()
            .map(|c| ToolCall {
                id: c.id.clone(),
                name: c.name.clone(),
                arguments: c.arguments.clone(),
                ..ToolCall::default()
            })
            .collect();
        out.messages.push(msg);
    }
    out
}

fn to_tools(tools: &[ConfTool]) -> Vec<ToolDef> {
    tools
        .iter()
        .map(|t| ToolDef {
            name: t.name.clone(),
            description: t.description.clone(),
            parameters: (!t.parameters.is_empty())
                .then(|| serde_json::from_str(&t.parameters).unwrap()),
            ..ToolDef::default()
        })
        .collect()
}

/// Numbers by value: 0 and 0.0 are the same temperature.
fn numeric(v: Value) -> Value {
    match v {
        Value::Number(n) => json!(n.as_f64().unwrap()),
        Value::Array(a) => Value::Array(a.into_iter().map(numeric).collect()),
        Value::Object(o) => Value::Object(o.into_iter().map(|(k, v)| (k, numeric(v))).collect()),
        other => other,
    }
}

fn kind(e: &LlmError) -> (&'static str, u16) {
    match e {
        LlmError::Truncated { .. } => ("truncated", 0),
        LlmError::PermissionDenied { .. } if !e.is_auth() => ("permission_denied without auth", 0),
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

const OK_COMPLETION: &str = r#"{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}"#;
const OK_SSE: &str = "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n";

async fn drain_stream(s: &mut Box<dyn hop_top_kit::llm::TokenStream>) -> Result<(), LlmError> {
    loop {
        match s.next().await? {
            Some(t) if t.done => return Ok(()),
            Some(_) => {}
            None => return Ok(()),
        }
    }
}

#[tokio::test]
async fn request_vectors() {
    let c = load();
    assert!(!c.openai_request.is_empty());
    for v in &c.openai_request {
        let streaming = v.call.ends_with("stream") || v.call == "stream_tools";
        let canned = if streaming {
            Canned::sse(OK_SSE)
        } else {
            Canned::json(200, OK_COMPLETION)
        };
        let srv = serve(vec![canned]).await;
        let a = adapter(&srv.url, &v.adapter_model, BTreeMap::new());
        let req = to_request(&v.request);
        let tools = to_tools(&v.tools);
        let res: Result<(), LlmError> = async {
            match v.call.as_str() {
                "complete" => a.complete(&req).await.map(|_| ()),
                "tools" => a.call_with_tools(&req, &tools).await.map(|_| ()),
                "stream" => drain_stream(&mut a.stream(&req).await?).await,
                "stream_tools" => drain_stream(&mut a.stream_with_tools(&req, &tools).await?).await,
                other => panic!("unknown call {other}"),
            }
        }
        .await;
        res.unwrap_or_else(|e| panic!("{}: {e}", v.name));
        let got = srv.only();
        assert_eq!(got.path, "/chat/completions", "{}", v.name);
        assert_eq!(
            numeric(got.json()),
            numeric(v.want_body.clone()),
            "{}",
            v.name
        );
    }
}

fn sse_of(c: &Corpus, sse: &str, from: &str, name: &str) -> String {
    if from.is_empty() {
        return sse.to_string();
    }
    c.openai_stream
        .iter()
        .find(|s| format!("openai_stream/{}", s.name) == from)
        .unwrap_or_else(|| panic!("{name}: sse_from {from}"))
        .sse
        .clone()
}

#[tokio::test]
async fn stream_vectors() {
    let c = load();
    assert!(!c.openai_stream.is_empty());
    for v in &c.openai_stream {
        let srv = serve(vec![Canned::sse(&v.sse)]).await;
        let a = adapter(&srv.url, "m", BTreeMap::new());
        let mut s = a.stream(&hi()).await.unwrap();
        let mut tokens = Vec::new();
        let mut err = None;
        loop {
            match s.next().await {
                Ok(Some(t)) if t.done => {
                    assert!(matches!(s.next().await, Ok(None)), "{}: after done", v.name);
                    break;
                }
                Ok(Some(t)) => tokens.push(t.content),
                Ok(None) => break,
                Err(e) => {
                    err = Some(e);
                    break;
                }
            }
        }
        assert_eq!(tokens, v.want_tokens, "{}", v.name);
        assert_eq!(
            err.as_ref().map_or("", |e| kind(e).0),
            v.error_kind,
            "{}: {err:?}",
            v.name
        );
        if let Some(e) = err {
            assert!(!e.to_string().contains(CONF_KEY), "{}: {e}", v.name);
        }
    }
}

fn usage_json(u: &Usage) -> Value {
    json!({
        "prompt_tokens": u.prompt_tokens,
        "completion_tokens": u.completion_tokens,
        "cache_read_tokens": u.cache_read_tokens,
        "cache_creation_tokens": u.cache_creation_tokens,
        "total_tokens": u.total_tokens,
    })
}

fn event_json(name: &str, t: &hop_top_kit::llm::Token) -> Value {
    if t.done {
        return json!({ "type": "done" });
    }
    if let Some(ev) = &t.tool_call {
        let mut out = json!({ "type": "tool_call", "kind": ev.kind.as_str(), "index": ev.index });
        match ev.kind {
            ToolCallEventKind::Start => {
                out["id"] = json!(ev.id);
                out["name"] = json!(ev.name);
            }
            ToolCallEventKind::Delta => out["arguments"] = json!(ev.arguments_delta),
            ToolCallEventKind::End => {
                let call = ev
                    .call
                    .as_ref()
                    .unwrap_or_else(|| panic!("{name}: end without call"));
                out["id"] = json!(call.id);
                out["name"] = json!(call.name);
                out["arguments"] = serde_json::from_str(&call.arguments).unwrap();
                assert_eq!(call.id, ev.id, "{name}: end repeats the id");
                assert_eq!(call.name, ev.name, "{name}: end repeats the name");
            }
        }
        return out;
    }
    if let Some(u) = &t.usage {
        return json!({ "type": "usage", "usage": usage_json(u) });
    }
    json!({ "type": "text", "text": t.content })
}

#[tokio::test]
async fn stream_events_vectors() {
    let c = load();
    assert!(!c.openai_stream_events.is_empty());
    for v in &c.openai_stream_events {
        let sse = sse_of(&c, &v.sse, &v.sse_from, &v.name);
        let srv = serve(vec![Canned::sse(&sse)]).await;
        let a = adapter(&srv.url, "m", BTreeMap::new());
        let mut req = hi();
        req.stream_events = v.stream_events;
        let defs: Vec<ToolDef> = v.tools.iter().map(ToolDef::named).collect();
        let mut s = if defs.is_empty() {
            a.stream(&req).await
        } else {
            a.stream_with_tools(&req, &defs).await
        }
        .unwrap_or_else(|e| panic!("{}: {e}", v.name));
        let mut got = Vec::new();
        let mut ends: Vec<ToolCallEvent> = Vec::new();
        let mut last: Option<Usage> = None;
        let mut err = None;
        loop {
            match s.next().await {
                Ok(Some(t)) => {
                    got.push(event_json(&v.name, &t));
                    if let Some(ev) = t
                        .tool_call
                        .as_ref()
                        .filter(|e| e.kind == ToolCallEventKind::End)
                    {
                        ends.push(ev.clone());
                    }
                    if let Some(u) = t.usage {
                        last = Some(u);
                    }
                    if t.done {
                        assert!(matches!(s.next().await, Ok(None)), "{}: after done", v.name);
                        break;
                    }
                }
                Ok(None) => break,
                Err(e) => {
                    err = Some(e);
                    break;
                }
            }
        }
        assert_eq!(
            numeric(Value::Array(got)),
            numeric(Value::Array(v.want_events.clone())),
            "{}",
            v.name
        );
        assert_eq!(
            err.as_ref().map_or("", |e| kind(e).0),
            v.error_kind,
            "{}: {err:?}",
            v.name
        );
        if defs.is_empty() || err.is_some() {
            if err.is_some() {
                assert!(
                    s.outcome().is_none(),
                    "{}: no outcome after an error",
                    v.name
                );
            }
            continue;
        }
        let out = s.outcome().unwrap_or_else(|| panic!("{}: outcome", v.name));
        if v.stream_events {
            assert_eq!(out.tool_calls.len(), ends.len(), "{}", v.name);
            for ev in &ends {
                assert_eq!(
                    Some(&out.tool_calls[ev.index]),
                    ev.call.as_ref(),
                    "{}",
                    v.name
                );
            }
            if let Some(u) = last {
                assert_eq!(out.usage, u, "{}: last usage is the outcome's", v.name);
            }
        }
    }
}

#[tokio::test]
async fn accumulate_vectors() {
    let c = load();
    assert!(!c.openai_accumulate.is_empty());
    for v in &c.openai_accumulate {
        let sse = sse_of(&c, &v.sse, &v.sse_from, &v.name);
        let srv = serve(vec![Canned::sse(&sse)]).await;
        let a = adapter(&srv.url, "m", BTreeMap::new());
        let defs: Vec<ToolDef> = v.tools.iter().map(ToolDef::named).collect();
        let mut s = a.stream_with_tools(&hi(), &defs).await.unwrap();
        let res = drain_stream(&mut s).await;
        assert_eq!(v.want.error, res.is_err(), "{}: {res:?}", v.name);
        if v.want.error {
            assert!(
                s.outcome().is_none(),
                "{}: no outcome after an error",
                v.name
            );
            continue;
        }
        let out = s.outcome().unwrap();
        assert_eq!(out.content, v.want.text, "{}", v.name);
        assert_eq!(out.finish_reason, v.want.finish_reason, "{}", v.name);
        assert_eq!(
            out.usage,
            Usage {
                prompt_tokens: v.want.usage.prompt_tokens,
                completion_tokens: v.want.usage.completion_tokens,
                total_tokens: v.want.usage.total_tokens,
                ..Usage::default()
            },
            "{}",
            v.name
        );
        assert_eq!(out.tool_calls.len(), v.want.tool_calls.len(), "{}", v.name);
        for (got, want) in out.tool_calls.iter().zip(&v.want.tool_calls) {
            assert_eq!(got.id, want.id, "{}", v.name);
            assert_eq!(got.name, want.name, "{}", v.name);
            let args: Value = serde_json::from_str(&got.arguments).unwrap();
            assert_eq!(args, want.arguments, "{}", v.name);
        }
    }
}

#[tokio::test]
async fn error_vectors() {
    let c = load();
    assert!(!c.openai_error.is_empty());
    for v in &c.openai_error {
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
        let a = adapter(&srv.url, "m", BTreeMap::new());
        let call = async {
            if v.stream {
                match a.stream(&hi()).await {
                    Ok(mut s) => drain_stream(&mut s).await,
                    Err(e) => Err(e),
                }
            } else {
                a.complete(&hi()).await.map(|_| ())
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

#[tokio::test]
async fn limit_vectors() {
    let c = load();
    assert!(!c.openai_limits.is_empty());
    for v in &c.openai_limits {
        let srv = serve(vec![Canned {
            status: 200,
            headers: vec![("content-type".into(), v.content_type.clone())],
            body: v.body.clone(),
            hang: false,
        }])
        .await;
        let a = adapter(&srv.url, "m", v.params.clone());
        let res = if v.stream {
            match a.stream(&hi()).await {
                Ok(mut s) => drain_stream(&mut s).await,
                Err(e) => Err(e),
            }
        } else {
            a.complete(&hi()).await.map(|_| ())
        };
        assert_eq!(srv.attempts(), 1, "{}: an overrun is not retried", v.name);
        match res {
            Err(LlmError::ResponseTooLarge { .. }) => assert!(v.too_large, "{}", v.name),
            Err(e) => panic!("{}: {e}", v.name),
            Ok(()) => assert!(!v.too_large, "{}: not refused", v.name),
        }
    }
}
