//! The `/api/chat` body and response, as the Go adapter shapes them.

use std::collections::BTreeMap;

use base64::Engine as _;
use serde_json::{json, Map, Value};

use crate::llm::{
    check_tool_choice, LlmError, Message, Part, Request, Response, Role, StreamOutcome, Usage,
};

fn invalid(msg: String) -> LlmError {
    LlmError::Invalid(msg)
}

/// The JSON body for `req` against `default_model`. Ollama's chat call
/// here sends no tools, so a tool choice fails the request.
pub(crate) fn body(
    default_model: &str,
    req: &Request,
    stream: bool,
) -> Result<Value, LlmError> {
    check_tool_choice(req.tool_choice.as_ref(), &[])
        .map_err(|e| invalid(format!("ollama: {e}")))?;
    // Resolves a tool result's ToolCallID to the tool_name Ollama also
    // accepts.
    let mut call_names: BTreeMap<String, String> = BTreeMap::new();
    let mut msgs = Vec::with_capacity(req.messages.len());
    for m in &req.messages {
        let mut cm = message(m)?;
        linkage(m, &mut cm, &mut call_names)?;
        msgs.push(Value::Object(cm));
    }
    let model = if req.model.is_empty() {
        default_model
    } else {
        &req.model
    };
    let mut b = Map::new();
    b.insert("model".into(), json!(model));
    b.insert("messages".into(), Value::Array(msgs));
    b.insert("stream".into(), json!(stream));
    if let Some(t) = req.temperature {
        b.insert("options".into(), json!({ "temperature": t }));
    }
    Ok(Value::Object(b))
}

/// Adds tool-call linkage to `cm`: `tool_calls` on an assistant turn,
/// `tool_call_id` plus the resolved `tool_name` on a tool result. A tool
/// message without a ToolCallID passes through, since Ollama accepts one.
fn linkage(
    m: &Message,
    cm: &mut Map<String, Value>,
    call_names: &mut BTreeMap<String, String>,
) -> Result<(), LlmError> {
    if !m.tool_call_id.is_empty() && m.role != Role::Tool {
        return Err(invalid(format!(
            "ollama: ToolCallID is valid only on role tool, got {:?}",
            m.role.as_str()
        )));
    }
    if !m.tool_calls.is_empty() && m.role != Role::Assistant {
        return Err(invalid(format!(
            "ollama: ToolCalls are valid only on role assistant, got {:?}",
            m.role.as_str()
        )));
    }
    if m.role == Role::Tool {
        if !m.parts.is_empty() {
            return Err(invalid(format!(
                "ollama: tool result {:?} carries only Content",
                m.tool_call_id
            )));
        }
        if !m.tool_call_id.is_empty() {
            cm.insert("tool_call_id".into(), json!(m.tool_call_id));
        }
        if let Some(n) = call_names.get(&m.tool_call_id) {
            cm.insert("tool_name".into(), json!(n));
        }
        return Ok(());
    }
    let mut calls = Vec::new();
    for tc in &m.tool_calls {
        let args: Value = if tc.arguments.is_empty() {
            json!({})
        } else {
            serde_json::from_str(&tc.arguments).map_err(|_| {
                invalid(format!(
                    "ollama: tool call {:?} arguments are not valid JSON",
                    tc.id
                ))
            })?
        };
        let mut call = Map::new();
        if !tc.id.is_empty() {
            call.insert("id".into(), json!(tc.id));
            call_names.insert(tc.id.clone(), tc.name.clone());
        }
        call.insert(
            "function".into(),
            json!({ "name": tc.name, "arguments": args }),
        );
        calls.push(Value::Object(call));
    }
    if !calls.is_empty() {
        cm.insert("tool_calls".into(), Value::Array(calls));
    }
    Ok(())
}

/// A message as an Ollama chat message. Parts concatenate their text
/// into `content` and base64 their images into `images` (plain base64,
/// no data URI).
fn message(m: &Message) -> Result<Map<String, Value>, LlmError> {
    let mut out = Map::new();
    out.insert("role".into(), json!(m.role.as_str()));
    if m.parts.is_empty() {
        out.insert("content".into(), json!(m.content));
        return Ok(out);
    }
    let mut text = String::new();
    let mut images = Vec::new();
    for p in &m.parts {
        match p {
            Part::Text(t) => text.push_str(t),
            Part::Media(media) if media.kind == crate::llm::MediaKind::Image => images.push(
                base64::engine::general_purpose::STANDARD.encode(&media.data),
            ),
            Part::Media(media) => {
                return Err(LlmError::UnsupportedModality {
                    modality: media.kind.as_str().into(),
                    provider: "ollama".into(),
                })
            }
        }
    }
    out.insert("content".into(), json!(text));
    if !images.is_empty() {
        out.insert("images".into(), json!(images));
    }
    Ok(out)
}

/// One chat response (or stream line).
pub(crate) struct Chat {
    pub content: String,
    pub role: String,
    pub done: bool,
    pub usage: Usage,
}

pub(crate) fn parse_chat(v: &Value) -> Chat {
    let n = |k: &str| v[k].as_u64().unwrap_or(0);
    Chat {
        content: v["message"]["content"].as_str().unwrap_or_default().into(),
        role: v["message"]["role"].as_str().unwrap_or_default().into(),
        done: v["done"].as_bool().unwrap_or(false),
        usage: Usage::new(n("prompt_eval_count"), n("eval_count")),
    }
}

impl Chat {
    pub fn response(self) -> Response {
        Response {
            content: self.content,
            role: match self.role.as_str() {
                "user" => Role::User,
                "system" => Role::System,
                "tool" => Role::Tool,
                _ => Role::Assistant,
            },
            usage: self.usage,
            finish_reason: "stop".into(),
            thinking: Vec::new(),
        }
    }
}

/// The outcome of a finished stream.
pub(crate) fn outcome(content: String, usage: Usage) -> StreamOutcome {
    StreamOutcome {
        content,
        finish_reason: "stop".into(),
        usage,
        ..StreamOutcome::default()
    }
}
