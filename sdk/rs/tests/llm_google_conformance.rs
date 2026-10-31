//! Runs the gemini_* sections of contracts/llm-v1 against the Google
//! adapter, over a loopback server. The vectors are the source of truth.

#![cfg(feature = "llm-google")]

mod llm_support;

use std::path::PathBuf;

use hop_top_kit::llm::google::Adapter;
use hop_top_kit::llm::{
    LlmError, Message, Provider, ProviderConfig, Request, ResolvedConfig, Token, ToolCall,
    ToolCallEvent, ToolCallEventKind, ToolDef, Usage,
};
use llm_support::{serve, Canned};
use serde::Deserialize;
use serde_json::{json, Value};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Case {
    name: String,
    sse: String,
    tools: Vec<String>,
    stream_events: bool,
    want_events: Vec<Value>,
    #[serde(default)]
    want_outcome: Option<Value>,
    error_kind: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Corpus {
    gemini_stream_events: Vec<Case>,
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
        .filter(|(k, _)| k.starts_with("gemini_"))
        .map(|(k, v)| (k.clone(), v.clone()))
        .collect();
    serde_json::from_value(Value::Object(only)).expect("gemini_* sections decode strictly")
}

fn usage_json(u: &Usage) -> Value {
    json!({
        "prompt_tokens": u.prompt_tokens, "completion_tokens": u.completion_tokens,
        "cache_read_tokens": u.cache_read_tokens, "cache_creation_tokens": u.cache_creation_tokens,
        "total_tokens": u.total_tokens,
    })
}

fn args_json(c: &ToolCall) -> Value {
    if c.arguments.is_empty() {
        Value::Null
    } else {
        serde_json::from_str(&c.arguments).unwrap()
    }
}

fn event_json(name: &str, t: &Token) -> Value {
    if t.done {
        assert!(t.content.is_empty(), "{name}: done carries no text");
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
                out["arguments"] = args_json(call);
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

fn kind(e: &LlmError) -> &'static str {
    match e {
        LlmError::Truncated { .. } => "truncated",
        LlmError::PermissionDenied { .. } => "permission_denied",
        LlmError::Auth { .. } => "auth",
        LlmError::RateLimit { .. } => "rate_limit",
        LlmError::Model { .. } => "model",
        LlmError::HttpStatus { .. } => "http_status",
        _ => "other",
    }
}

#[tokio::test]
async fn gemini_stream_events_vectors() {
    let c = load();
    assert!(!c.gemini_stream_events.is_empty());
    for v in &c.gemini_stream_events {
        let srv = serve(vec![Canned::sse(&v.sse)]).await;
        let a = Adapter::new(ResolvedConfig {
            provider: ProviderConfig {
                api_key: "k".into(),
                base_url: srv.url.clone(),
                model: "m".into(),
                ..ProviderConfig::default()
            },
            ..ResolvedConfig::default()
        })
        .unwrap();
        let req = Request {
            messages: vec![Message::user("Hi")],
            stream_events: v.stream_events,
            ..Request::default()
        };
        let defs: Vec<ToolDef> = v.tools.iter().map(ToolDef::named).collect();
        let mut s = if defs.is_empty() {
            a.stream(&req).await
        } else {
            a.stream_with_tools(&req, &defs).await
        }
        .unwrap_or_else(|e| panic!("{}: {e}", v.name));
        let mut got = Vec::new();
        let mut ends: Vec<ToolCallEvent> = Vec::new();
        let mut last = None;
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
            Value::Array(got),
            Value::Array(v.want_events.clone()),
            "{}",
            v.name
        );
        assert_eq!(
            err.as_ref().map_or("", kind),
            v.error_kind,
            "{}: {err:?}",
            v.name
        );
        if defs.is_empty() {
            assert!(
                v.want_outcome.is_none(),
                "{}: want_outcome needs tools",
                v.name
            );
            continue;
        }
        if err.is_some() {
            assert!(
                s.outcome().is_none(),
                "{}: no outcome after an error",
                v.name
            );
            continue;
        }
        let out = s.outcome().unwrap_or_else(|| panic!("{}: outcome", v.name));
        let calls: Vec<Value> = out
            .tool_calls
            .iter()
            .map(|c| json!({ "id": c.id, "name": c.name, "arguments": args_json(c) }))
            .collect();
        assert_eq!(
            json!({ "content": out.content, "tool_calls": calls,
                    "finish_reason": out.finish_reason, "usage": usage_json(&out.usage) }),
            v.want_outcome.clone().unwrap(),
            "{}",
            v.name
        );
        assert!(out.thinking.is_empty());
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
                assert_eq!(out.usage, u, "{}", v.name);
            }
        }
    }
}
