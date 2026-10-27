//! Request shaping and response mapping for the Messages API, as the Go
//! adapter does it. Pinned by `contracts/llm-v1` (`anthropic_request`,
//! `anthropic_request_error`).

use base64::Engine as _;
use serde_json::{json, Map, Value};

use crate::llm::{
    LlmError, Media, Message, Part, Request, Response, Role, ToolCall, ToolDef, ToolResponse, Usage,
};

use super::{DEFAULT_MAX_TOKENS, SCHEME};

fn invalid(msg: String) -> LlmError {
    LlmError::Invalid(msg)
}

/// The JSON body for `req` against `default_model`. `tools` adds tool
/// definitions; `stream` asks for SSE.
pub(crate) fn body(
    default_model: &str,
    req: &Request,
    tools: Option<&[ToolDef]>,
    stream: bool,
) -> Result<Value, LlmError> {
    let (system, messages) = messages(&req.messages)?;
    let model = if req.model.is_empty() {
        default_model
    } else {
        &req.model
    };
    let max_tokens = if req.max_tokens == 0 {
        DEFAULT_MAX_TOKENS
    } else {
        req.max_tokens
    };
    let mut b = Map::new();
    b.insert("model".into(), json!(model));
    b.insert("max_tokens".into(), json!(max_tokens));
    b.insert("messages".into(), Value::Array(messages));
    if let Some(s) = system {
        b.insert("system".into(), json!([{ "type": "text", "text": s }]));
    }
    if let Some(t) = req.temperature {
        b.insert("temperature".into(), json!(t));
    }
    if !req.stop_sequences.is_empty() {
        b.insert("stop_sequences".into(), json!(req.stop_sequences));
    }
    if let Some(tools) = tools {
        b.insert(
            "tools".into(),
            Value::Array(tools.iter().map(tool_def).collect()),
        );
    }
    if stream {
        b.insert("stream".into(), Value::Bool(true));
    }
    Ok(Value::Object(b))
}

fn text_block(text: &str) -> Value {
    json!({ "type": "text", "text": text })
}

/// The top-level system text (the last system message wins, as in Go)
/// and the conversation without it.
fn messages(msgs: &[Message]) -> Result<(Option<String>, Vec<Value>), LlmError> {
    let mut system = None;
    let mut out: Vec<Value> = Vec::with_capacity(msgs.len());
    for (i, m) in msgs.iter().enumerate() {
        if m.role == Role::System {
            system = Some(m.content.clone());
            continue;
        }
        check_linkage_role(m)?;
        if m.role == Role::Tool {
            let block = tool_result(m)?;
            // Consecutive results answer one assistant turn: one user turn.
            if i > 0 && msgs[i - 1].role == Role::Tool {
                if let Some(Value::Array(content)) =
                    out.last_mut().and_then(|last| last.get_mut("content"))
                {
                    content.push(block);
                    continue;
                }
            }
            out.push(json!({ "role": "user", "content": [block] }));
            continue;
        }
        let mut blocks = if !m.parts.is_empty() {
            parts(&m.parts)?
        } else if m.tool_calls.is_empty() || !m.content.is_empty() {
            vec![text_block(&m.content)]
        } else {
            Vec::new()
        };
        for tc in &m.tool_calls {
            blocks.push(tool_use(tc)?);
        }
        out.push(json!({ "role": m.role.as_str(), "content": blocks }));
    }
    Ok((system, out))
}

fn check_linkage_role(m: &Message) -> Result<(), LlmError> {
    if !m.tool_calls.is_empty() && m.role != Role::Assistant {
        return Err(invalid(format!(
            "{SCHEME}: ToolCalls are valid only on role assistant, got {:?}",
            m.role.as_str()
        )));
    }
    if !m.tool_call_id.is_empty() && m.role != Role::Tool {
        return Err(invalid(format!(
            "{SCHEME}: ToolCallID is valid only on role tool, got {:?}",
            m.role.as_str()
        )));
    }
    Ok(())
}

fn tool_result(m: &Message) -> Result<Value, LlmError> {
    if m.tool_call_id.is_empty() {
        return Err(invalid(format!("{SCHEME}: tool result needs ToolCallID")));
    }
    if !m.parts.is_empty() {
        return Err(invalid(format!(
            "{SCHEME}: tool result {:?} carries only Content",
            m.tool_call_id
        )));
    }
    let mut b = json!({ "type": "tool_result", "tool_use_id": m.tool_call_id });
    // The API rejects empty text blocks: an empty result sends none.
    if !m.content.is_empty() {
        b["content"] = json!([text_block(&m.content)]);
    }
    Ok(b)
}

fn tool_use(tc: &ToolCall) -> Result<Value, LlmError> {
    if tc.id.is_empty() {
        return Err(invalid(format!(
            "{SCHEME}: tool call {:?} needs an ID",
            tc.name
        )));
    }
    let input: Value = if tc.arguments.is_empty() {
        json!({})
    } else {
        serde_json::from_str(&tc.arguments).map_err(|_| {
            invalid(format!(
                "{SCHEME}: tool call {:?} arguments are not valid JSON",
                tc.id
            ))
        })?
    };
    Ok(json!({ "type": "tool_use", "id": tc.id, "name": tc.name, "input": input }))
}

fn parts(parts: &[Part]) -> Result<Vec<Value>, LlmError> {
    parts
        .iter()
        .map(|p| match p {
            Part::Text(t) => Ok(text_block(t)),
            Part::Media(m) => media(m),
        })
        .collect()
}

fn media(m: &Media) -> Result<Value, LlmError> {
    if m.kind != crate::llm::MediaKind::Image {
        return Err(LlmError::UnsupportedModality {
            modality: m.kind.as_str().into(),
            provider: SCHEME.into(),
        });
    }
    if m.mime_type.is_empty() {
        return Err(LlmError::InvalidFormat {
            format: String::new(),
            provider: SCHEME.into(),
        });
    }
    let data = base64::engine::general_purpose::STANDARD.encode(&m.data);
    let kind = if m.mime_type == "application/pdf" {
        "document"
    } else {
        "image"
    };
    Ok(json!({
        "type": kind,
        "source": { "type": "base64", "media_type": m.mime_type, "data": data },
    }))
}

/// A tool definition. The API requires `input_schema`: a tool without
/// parameters (or with a non-object schema) sends
/// `{"type":"object","properties":{}}`, as Go does; a schema without
/// `type` gets `"object"`.
fn tool_def(d: &ToolDef) -> Value {
    let mut schema = match &d.parameters {
        Some(Value::Object(m)) => m.clone(),
        _ => {
            let mut m = Map::new();
            m.insert("properties".into(), json!({}));
            m
        }
    };
    schema
        .entry("type")
        .or_insert_with(|| Value::String("object".into()));
    json!({ "name": d.name, "description": d.description, "input_schema": schema })
}

/// A non-streamed Messages API response.
pub(crate) struct Parsed {
    pub content: String,
    pub tool_calls: Vec<ToolCall>,
    pub stop_reason: String,
    pub usage: Usage,
}

pub(crate) fn parse_message(v: &Value) -> Parsed {
    let mut out = Parsed {
        content: String::new(),
        tool_calls: Vec::new(),
        stop_reason: v["stop_reason"].as_str().unwrap_or_default().to_string(),
        usage: Usage::new(
            v["usage"]["input_tokens"].as_u64().unwrap_or(0),
            v["usage"]["output_tokens"].as_u64().unwrap_or(0),
        ),
    };
    for block in v["content"].as_array().into_iter().flatten() {
        match block["type"].as_str() {
            Some("text") => out
                .content
                .push_str(block["text"].as_str().unwrap_or_default()),
            Some("tool_use") => out.tool_calls.push(ToolCall {
                id: block["id"].as_str().unwrap_or_default().into(),
                name: block["name"].as_str().unwrap_or_default().into(),
                arguments: block.get("input").map(Value::to_string).unwrap_or_default(),
                ..ToolCall::default()
            }),
            _ => {}
        }
    }
    out
}

impl Parsed {
    pub fn response(self) -> Response {
        Response {
            content: self.content,
            role: Role::Assistant,
            usage: self.usage,
            finish_reason: self.stop_reason,
        }
    }

    pub fn tool_response(self) -> ToolResponse {
        ToolResponse {
            content: self.content,
            tool_calls: self.tool_calls,
        }
    }
}
