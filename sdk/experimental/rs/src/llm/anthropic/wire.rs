//! Request shaping and response mapping for the Messages API, as the Go
//! adapter does it. Pinned by `contracts/llm-v1` (`anthropic_request`,
//! `anthropic_request_error`).

use base64::Engine as _;
use serde_json::{json, Map, Value};

use crate::llm::{
    check_tool_choice, CacheControl, LlmError, Media, Message, Part, Request, Response, Role,
    ThinkingBlock, ToolCall, ToolChoice, ToolChoiceMode, ToolDef, ToolNames, ToolResponse, Usage,
};

use super::{DEFAULT_MAX_TOKENS, SCHEME};

fn invalid(msg: String) -> LlmError {
    LlmError::Invalid(msg)
}

/// The JSON body for `req` against `default_model`. `tools` adds tool
/// definitions; `stream` asks for SSE. Tool definitions and replayed
/// calls go out under their `names` wire names.
pub(crate) fn body(
    default_model: &str,
    req: &Request,
    tools: Option<&[ToolDef]>,
    stream: bool,
    names: &ToolNames,
) -> Result<Value, LlmError> {
    let (system, messages) = messages(&req.messages, names)?;
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
    if !system.is_empty() {
        b.insert("system".into(), Value::Array(system));
    }
    if let Some(t) = req.thinking {
        if t.budget_tokens < MIN_THINKING_BUDGET {
            return Err(invalid(format!(
                "{SCHEME}: thinking budget_tokens must be at least {MIN_THINKING_BUDGET}, got {}",
                t.budget_tokens
            )));
        }
        if t.budget_tokens >= max_tokens {
            return Err(invalid(format!(
                "{SCHEME}: thinking budget_tokens ({}) must be less than max_tokens ({max_tokens})",
                t.budget_tokens
            )));
        }
        b.insert(
            "thinking".into(),
            json!({ "type": "enabled", "budget_tokens": t.budget_tokens }),
        );
    }
    if let Some(t) = req.temperature {
        b.insert("temperature".into(), json!(t));
    }
    if !req.stop_sequences.is_empty() {
        b.insert("stop_sequences".into(), json!(req.stop_sequences));
    }
    if let Some(tools) = tools {
        let defs = tools
            .iter()
            .map(|d| tool_def(d, names))
            .collect::<Result<_, _>>()?;
        b.insert("tools".into(), Value::Array(defs));
    }
    if let Some(choice) = tool_choice(req.tool_choice.as_ref(), tools.unwrap_or_default(), names)? {
        b.insert("tool_choice".into(), choice);
    }
    if stream {
        b.insert("stream".into(), Value::Bool(true));
    }
    Ok(Value::Object(b))
}

/// The body's `tool_choice` for `c`, checked against `tools`
/// ([`check_tool_choice`]); a named tool goes by the name the request
/// sends for it. `None` sends none.
fn tool_choice(
    c: Option<&ToolChoice>,
    tools: &[ToolDef],
    names: &ToolNames,
) -> Result<Option<Value>, LlmError> {
    check_tool_choice(c, tools).map_err(|e| invalid(format!("{SCHEME}: {e}")))?;
    let Some(c) = c else { return Ok(None) };
    Ok(Some(if c.mode == ToolChoiceMode::TOOL {
        json!({ "type": "tool", "name": names.wire(&c.name) })
    } else {
        json!({ "type": c.mode.as_str() })
    }))
}

fn text_block(text: &str) -> Value {
    json!({ "type": "text", "text": text })
}

/// The API's least thinking budget.
const MIN_THINKING_BUDGET: u32 = 1024;

/// `cache_control` for `cc`; `ttl` must be empty, "5m" or "1h".
fn cache_value(cc: &CacheControl) -> Result<Value, LlmError> {
    match cc.ttl.as_str() {
        "" => Ok(json!({ "type": "ephemeral" })),
        "5m" | "1h" => Ok(json!({ "type": "ephemeral", "ttl": cc.ttl })),
        other => Err(invalid(format!(
            "{SCHEME}: cache_control ttl must be \"5m\" or \"1h\", got {other:?}"
        ))),
    }
}

fn set_cache(block: &mut Value, cc: Option<&CacheControl>) -> Result<(), LlmError> {
    if let Some(cc) = cc {
        block["cache_control"] = cache_value(cc)?;
    }
    Ok(())
}

/// Thinking blocks back to request blocks, unchanged and in order.
fn replay_thinking(tbs: &[ThinkingBlock]) -> Vec<Value> {
    tbs.iter()
        .map(|t| {
            if t.redacted.is_empty() {
                json!({ "type": "thinking", "thinking": t.thinking, "signature": t.signature })
            } else {
                json!({ "type": "redacted_thinking", "data": t.redacted })
            }
        })
        .collect()
}

/// Joins consecutive system messages into one text.
const SYSTEM_SEPARATOR: &str = "\n\n";

/// Every system message, wherever it sits, as the system field's text
/// blocks: their content joined in order with a blank line
/// ([`SYSTEM_SEPARATOR`]), empty ones skipped. A cache breakpoint on a
/// system message ends a block there and carries that breakpoint, so
/// each breakpoint caches exactly the text up to it; the next system
/// message starts a new block. No system text yields none.
fn system_blocks(msgs: &[Message]) -> Result<Vec<Value>, LlmError> {
    let mut out = Vec::new();
    let mut text = String::new();
    for m in msgs.iter().filter(|m| m.role == Role::System) {
        if !m.content.is_empty() {
            if !text.is_empty() {
                text.push_str(SYSTEM_SEPARATOR);
            }
            text.push_str(&m.content);
        }
        let Some(cc) = &m.cache_control else { continue };
        if text.is_empty() {
            continue;
        }
        let mut block = text_block(&text);
        set_cache(&mut block, Some(cc))?;
        out.push(block);
        text.clear();
    }
    if !text.is_empty() {
        out.push(text_block(&text));
    }
    Ok(out)
}

/// The system field's blocks ([`system_blocks`]; Anthropic takes system
/// as a top-level field) and the conversation without them.
fn messages(msgs: &[Message], names: &ToolNames) -> Result<(Vec<Value>, Vec<Value>), LlmError> {
    let system = system_blocks(msgs)?;
    let mut out: Vec<Value> = Vec::with_capacity(msgs.len());
    for (i, m) in msgs.iter().enumerate() {
        if m.role == Role::System {
            continue;
        }
        check_linkage_role(m)?;
        if m.role == Role::Tool {
            let mut block = tool_result(m)?;
            set_cache(&mut block, m.cache_control.as_ref())?;
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
            blocks.push(tool_use(tc, names)?);
        }
        if let Some(last) = blocks.last_mut() {
            set_cache(last, m.cache_control.as_ref())?;
        }
        let mut content = replay_thinking(&m.thinking);
        content.extend(blocks);
        out.push(json!({ "role": m.role.as_str(), "content": content }));
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
    if !m.thinking.is_empty() && m.role != Role::Assistant {
        return Err(invalid(format!(
            "{SCHEME}: Thinking is valid only on role assistant, got {:?}",
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

/// A replayed call, under the name the request sends for it.
fn tool_use(tc: &ToolCall, names: &ToolNames) -> Result<Value, LlmError> {
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
    Ok(json!({ "type": "tool_use", "id": tc.id, "name": names.wire(&tc.name), "input": input }))
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
fn tool_def(d: &ToolDef, names: &ToolNames) -> Result<Value, LlmError> {
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
    let mut def = json!({
        "name": names.wire(&d.name),
        "description": d.description,
        "input_schema": schema,
    });
    set_cache(&mut def, d.cache_control.as_ref())?;
    Ok(def)
}

/// A non-streamed Messages API response.
pub(crate) struct Parsed {
    pub content: String,
    pub thinking: Vec<ThinkingBlock>,
    pub tool_calls: Vec<ToolCall>,
    pub stop_reason: String,
    pub usage: Usage,
}

pub(crate) fn parse_message(v: &Value) -> Parsed {
    let mut out = Parsed {
        content: String::new(),
        thinking: Vec::new(),
        tool_calls: Vec::new(),
        stop_reason: v["stop_reason"].as_str().unwrap_or_default().to_string(),
        usage: usage_of(&v["usage"]),
    };
    for block in v["content"].as_array().into_iter().flatten() {
        match block["type"].as_str() {
            Some("text") => out
                .content
                .push_str(block["text"].as_str().unwrap_or_default()),
            Some("thinking") => out.thinking.push(ThinkingBlock {
                thinking: block["thinking"].as_str().unwrap_or_default().into(),
                signature: block["signature"].as_str().unwrap_or_default().into(),
                redacted: String::new(),
            }),
            Some("redacted_thinking") => out.thinking.push(ThinkingBlock {
                redacted: block["data"].as_str().unwrap_or_default().into(),
                ..ThinkingBlock::default()
            }),
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

/// Usage from a Messages API `usage` object, cache tokens included.
pub(crate) fn usage_of(u: &Value) -> Usage {
    let n = |k: &str| u[k].as_u64().unwrap_or(0);
    Usage::with_cache(
        n("input_tokens"),
        n("output_tokens"),
        n("cache_read_input_tokens"),
        n("cache_creation_input_tokens"),
    )
}

impl Parsed {
    pub fn response(self) -> Response {
        Response {
            content: self.content,
            role: Role::Assistant,
            usage: self.usage,
            finish_reason: self.stop_reason,
            thinking: self.thinking,
        }
    }

    pub fn tool_response(self) -> ToolResponse {
        ToolResponse {
            content: self.content,
            tool_calls: self.tool_calls,
            thinking: self.thinking,
            finish_reason: self.stop_reason,
            usage: self.usage,
        }
    }
}
