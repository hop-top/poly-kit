//! Request shaping and response mapping for the Chat Completions API,
//! as the Go adapter does it. Pinned by `contracts/llm-v1`
//! (`openai_request`).

use base64::Engine as _;
use serde_json::{json, Map, Value};

use crate::llm::{
    check_tool_choice, LlmError, Media, MediaKind, Message, Part, Request, Response, Role,
    ToolCall, ToolChoice, ToolChoiceMode, ToolDef, ToolNames, ToolResponse, Usage,
};

fn invalid(msg: String) -> LlmError {
    LlmError::Invalid(msg)
}

/// The JSON body for `req` against `default_model`. `tools` adds tool
/// definitions; `stream` asks for SSE (and, with `req.stream_events`,
/// for the usage chunk). Tool definitions and replayed calls go out
/// under their `names` wire names.
pub(crate) fn body(
    scheme: &str,
    default_model: &str,
    req: &Request,
    tools: Option<&[ToolDef]>,
    stream: bool,
    names: &ToolNames,
) -> Result<Value, LlmError> {
    check_tool_choice(req.tool_choice.as_ref(), tools.unwrap_or_default())
        .map_err(|e| invalid(format!("{scheme}: {e}")))?;
    let model = if req.model.is_empty() {
        default_model
    } else {
        &req.model
    };
    let mut b = Map::new();
    b.insert("model".into(), json!(model));
    b.insert(
        "messages".into(),
        Value::Array(messages(&req.messages, names)?),
    );
    if let Some(t) = req.temperature {
        b.insert("temperature".into(), json!(t));
    }
    if req.max_tokens > 0 {
        b.insert("max_tokens".into(), json!(req.max_tokens));
    }
    if !req.stop_sequences.is_empty() {
        b.insert("stop".into(), json!(req.stop_sequences));
    }
    if let Some(c) = &req.tool_choice {
        b.insert("tool_choice".into(), tool_choice(c, names));
    }
    if let Some(tools) = tools {
        b.insert(
            "tools".into(),
            Value::Array(tools.iter().map(|d| tool_def(d, names)).collect()),
        );
    }
    if stream {
        b.insert("stream".into(), Value::Bool(true));
        // Usage streams only when asked for, in a chunk after the last
        // choice; a stream without events does not ask.
        if req.stream_events {
            b.insert("stream_options".into(), json!({ "include_usage": true }));
        }
    }
    Ok(Value::Object(b))
}

/// A checked `c`: any is `"required"`, a named tool goes by the name the
/// request sends for it.
fn tool_choice(c: &ToolChoice, names: &ToolNames) -> Value {
    if c.mode == ToolChoiceMode::ANY {
        json!("required")
    } else if c.mode == ToolChoiceMode::TOOL {
        json!({ "type": "function", "function": { "name": names.wire(&c.name) } })
    } else {
        json!(c.mode.as_str())
    }
}

fn messages(msgs: &[Message], names: &ToolNames) -> Result<Vec<Value>, LlmError> {
    let mut out = Vec::with_capacity(msgs.len());
    for m in msgs {
        if let Some(linked) = tool_linkage(m, names)? {
            out.push(linked);
            continue;
        }
        if m.parts.is_empty() {
            let role = match m.role {
                Role::System => "system",
                Role::Assistant => "assistant",
                _ => "user",
            };
            out.push(json!({ "role": role, "content": m.content }));
            continue;
        }
        // Multimodal: only user messages take content parts.
        if m.role != Role::User {
            return Err(invalid(format!(
                "openai: message role {:?} does not support content parts",
                m.role.as_str()
            )));
        }
        out.push(json!({ "role": "user", "content": content_parts(&m.parts)? }));
    }
    Ok(out)
}

/// The tool-call half of a conversation: an assistant turn carrying
/// calls becomes a message with `tool_calls`, a tool result a tool
/// message with `tool_call_id`. `None` for any other message.
fn tool_linkage(m: &Message, names: &ToolNames) -> Result<Option<Value>, LlmError> {
    if m.role == Role::Tool {
        if m.tool_call_id.is_empty() {
            return Err(invalid("openai: tool result needs ToolCallID".into()));
        }
        if !m.tool_calls.is_empty() || !m.parts.is_empty() {
            return Err(invalid(format!(
                "openai: tool result {:?} carries only Content",
                m.tool_call_id
            )));
        }
        return Ok(Some(
            json!({ "role": "tool", "content": m.content, "tool_call_id": m.tool_call_id }),
        ));
    }
    if !m.tool_call_id.is_empty() {
        return Err(invalid(format!(
            "openai: ToolCallID is valid only on role tool, got {:?}",
            m.role.as_str()
        )));
    }
    if m.tool_calls.is_empty() {
        return Ok(None);
    }
    if m.role != Role::Assistant {
        return Err(invalid(format!(
            "openai: ToolCalls are valid only on role assistant, got {:?}",
            m.role.as_str()
        )));
    }
    if !m.parts.is_empty() {
        return Err(invalid(
            "openai: assistant tool calls do not support content parts".into(),
        ));
    }
    let mut calls = Vec::with_capacity(m.tool_calls.len());
    for tc in &m.tool_calls {
        calls.push(replayed_call(tc, names)?);
    }
    let mut msg = Map::new();
    msg.insert("role".into(), json!("assistant"));
    if !m.content.is_empty() {
        msg.insert("content".into(), json!(m.content));
    }
    msg.insert("tool_calls".into(), Value::Array(calls));
    Ok(Some(Value::Object(msg)))
}

fn replayed_call(tc: &ToolCall, names: &ToolNames) -> Result<Value, LlmError> {
    if tc.id.is_empty() {
        return Err(invalid(format!(
            "openai: tool call {:?} needs an ID",
            tc.name
        )));
    }
    let args = if tc.arguments.is_empty() {
        "{}".to_string()
    } else if serde_json::from_str::<Value>(&tc.arguments).is_err() {
        return Err(invalid(format!(
            "openai: tool call {:?} arguments are not valid JSON",
            tc.id
        )));
    } else {
        tc.arguments.clone()
    };
    Ok(json!({
        "id": tc.id,
        "type": "function",
        "function": { "name": names.wire(&tc.name), "arguments": args },
    }))
}

fn content_parts(parts: &[Part]) -> Result<Vec<Value>, LlmError> {
    parts
        .iter()
        .map(|p| match p {
            Part::Text(t) => Ok(json!({ "type": "text", "text": t })),
            Part::Media(m) => media(m),
        })
        .collect()
}

fn media(m: &Media) -> Result<Value, LlmError> {
    if m.kind != MediaKind::Image {
        return Err(LlmError::UnsupportedModality {
            modality: m.kind.as_str().into(),
            provider: "openai".into(),
        });
    }
    if m.mime_type.is_empty() {
        return Err(LlmError::InvalidFormat {
            format: String::new(),
            provider: "openai".into(),
        });
    }
    let data = base64::engine::general_purpose::STANDARD.encode(&m.data);
    if m.mime_type == "application/pdf" {
        return Ok(json!({ "type": "file", "file": { "file_data": data } }));
    }
    Ok(json!({
        "type": "image_url",
        "image_url": { "url": format!("data:{};base64,{data}", m.mime_type) },
    }))
}

/// A function tool. A description is sent when set; parameters when
/// they are a JSON object.
fn tool_def(d: &ToolDef, names: &ToolNames) -> Value {
    let mut f = Map::new();
    f.insert("name".into(), json!(names.wire(&d.name)));
    if !d.description.is_empty() {
        f.insert("description".into(), json!(d.description));
    }
    if let Some(p @ Value::Object(_)) = &d.parameters {
        f.insert("parameters".into(), p.clone());
    }
    json!({ "type": "function", "function": f })
}

/// A non-streamed Chat Completions response.
pub(crate) struct Parsed {
    pub content: String,
    pub role: Role,
    pub tool_calls: Vec<ToolCall>,
    pub finish_reason: String,
    pub usage: Usage,
}

pub(crate) fn usage_of(u: &Value) -> Usage {
    let n = |k: &str| u[k].as_u64().unwrap_or(0);
    Usage {
        prompt_tokens: n("prompt_tokens"),
        completion_tokens: n("completion_tokens"),
        total_tokens: n("total_tokens"),
        ..Usage::default()
    }
}

pub(crate) fn parse_completion(v: &Value) -> Parsed {
    let mut out = Parsed {
        content: String::new(),
        role: Role::Assistant,
        tool_calls: Vec::new(),
        finish_reason: String::new(),
        usage: usage_of(&v["usage"]),
    };
    if let Some(choice) = v["choices"].as_array().and_then(|c| c.first()) {
        let m = &choice["message"];
        out.content = m["content"].as_str().unwrap_or_default().to_string();
        out.role = match m["role"].as_str() {
            Some("user") => Role::User,
            Some("system") => Role::System,
            Some("tool") => Role::Tool,
            _ => Role::Assistant,
        };
        out.finish_reason = choice["finish_reason"].as_str().unwrap_or_default().into();
        for tc in m["tool_calls"].as_array().into_iter().flatten() {
            out.tool_calls.push(ToolCall {
                id: tc["id"].as_str().unwrap_or_default().into(),
                name: tc["function"]["name"].as_str().unwrap_or_default().into(),
                arguments: tc["function"]["arguments"]
                    .as_str()
                    .unwrap_or_default()
                    .into(),
                ..ToolCall::default()
            });
        }
    }
    out
}

impl Parsed {
    pub fn response(self) -> Response {
        Response {
            content: self.content,
            role: self.role,
            usage: self.usage,
            finish_reason: self.finish_reason,
            thinking: Vec::new(),
        }
    }

    pub fn tool_response(self) -> ToolResponse {
        ToolResponse {
            content: self.content,
            tool_calls: self.tool_calls,
            thinking: Vec::new(),
            finish_reason: self.finish_reason,
            usage: self.usage,
        }
    }
}
