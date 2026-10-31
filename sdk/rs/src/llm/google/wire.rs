//! Request shaping and response mapping for the Gemini
//! `generateContent` API, as the Go adapter does it.

use std::collections::BTreeMap;

use base64::Engine as _;
use serde_json::{json, Map, Value};

use crate::llm::{
    check_tool_choice, LlmError, Media, MediaKind, Message, Part, ProviderData, Request, Response,
    Role, ToolCall, ToolChoice, ToolChoiceMode, ToolDef, ToolNames, ToolResponse, Usage,
};

/// The ProviderData namespace the adapter keeps its opaque state under.
pub(crate) const PROVIDER_DATA_KEY: &str = "google";

/// The documented value that skips thought-signature validation for a
/// function call the model did not sign (hand-built or other-provider
/// history in the current turn).
const SKIP_THOUGHT_SIGNATURE: &str = "skip_thought_signature_validator";

fn invalid(msg: String) -> LlmError {
    LlmError::Invalid(msg)
}

/// The JSON body for `req`. `tools` adds function declarations.
pub(crate) fn body(req: &Request, tools: &[ToolDef], names: &ToolNames) -> Result<Value, LlmError> {
    check_tool_choice(req.tool_choice.as_ref(), tools)
        .map_err(|e| invalid(format!("gemini: {e}")))?;
    let mut contents: Vec<Value> = Vec::with_capacity(req.messages.len());
    let mut system: Vec<Value> = Vec::new();
    let mut call_names: BTreeMap<String, String> = BTreeMap::new();
    let turn = current_turn_start(&req.messages);
    for (i, m) in req.messages.iter().enumerate() {
        if m.role == Role::System {
            system.push(json!({ "text": m.content }));
            continue;
        }
        if m.role == Role::Tool {
            let p = function_response_part(m, &call_names, names)?;
            // Consecutive results answer one model turn: one user turn.
            if i > 0 && req.messages[i - 1].role == Role::Tool {
                if let Some(Value::Array(parts)) =
                    contents.last_mut().and_then(|c| c.get_mut("parts"))
                {
                    parts.push(p);
                    continue;
                }
            }
            contents.push(json!({ "role": "user", "parts": [p] }));
            continue;
        }
        let c = map_message(m, i >= turn, names)?;
        for tc in &m.tool_calls {
            call_names.insert(tc.id.clone(), tc.name.clone());
        }
        contents.push(c);
    }
    let mut b = Map::new();
    b.insert("contents".into(), Value::Array(contents));
    if !system.is_empty() {
        b.insert(
            "systemInstruction".into(),
            json!({ "role": "user", "parts": system }),
        );
    }
    let mut gc = Map::new();
    if let Some(t) = req.temperature {
        gc.insert("temperature".into(), json!(t));
    }
    if req.max_tokens > 0 {
        gc.insert("maxOutputTokens".into(), json!(req.max_tokens));
    }
    if !req.stop_sequences.is_empty() {
        gc.insert("stopSequences".into(), json!(req.stop_sequences));
    }
    if !gc.is_empty() {
        b.insert("generationConfig".into(), Value::Object(gc));
    }
    if !tools.is_empty() {
        let decls: Vec<Value> = tools
            .iter()
            .map(|t| {
                let mut d = Map::new();
                d.insert("name".into(), json!(names.wire(&t.name)));
                d.insert("description".into(), json!(t.description));
                if let Some(p) = &t.parameters {
                    d.insert("parameters".into(), p.clone());
                }
                Value::Object(d)
            })
            .collect();
        b.insert("tools".into(), json!([{ "functionDeclarations": decls }]));
    }
    if let Some(c) = &req.tool_choice {
        b.insert("toolConfig".into(), tool_config(c, names));
    }
    Ok(Value::Object(b))
}

fn tool_config(c: &ToolChoice, names: &ToolNames) -> Value {
    let fc = if c.mode == ToolChoiceMode::AUTO {
        json!({ "mode": "AUTO" })
    } else if c.mode == ToolChoiceMode::ANY {
        json!({ "mode": "ANY" })
    } else if c.mode == ToolChoiceMode::NONE {
        json!({ "mode": "NONE" })
    } else {
        json!({ "mode": "ANY", "allowedFunctionNames": [names.wire(&c.name)] })
    };
    json!({ "functionCallingConfig": fc })
}

/// Index of the last user message: the start of the current turn.
fn current_turn_start(msgs: &[Message]) -> usize {
    msgs.iter().rposition(|m| m.role == Role::User).unwrap_or(0)
}

fn map_message(m: &Message, current_turn: bool, names: &ToolNames) -> Result<Value, LlmError> {
    if !m.tool_call_id.is_empty() {
        return Err(invalid(format!(
            "gemini: ToolCallID is valid only on role tool, got {:?}",
            m.role.as_str()
        )));
    }
    if !m.tool_calls.is_empty() && m.role != Role::Assistant {
        return Err(invalid(format!(
            "gemini: ToolCalls are valid only on role assistant, got {:?}",
            m.role.as_str()
        )));
    }
    let role = if m.role == Role::Assistant {
        "model"
    } else {
        m.role.as_str()
    };
    let mut parts = if m.parts.is_empty() {
        vec![json!({ "text": m.content })]
    } else {
        content_parts(&m.parts)?
    };
    if m.tool_calls.is_empty() {
        return Ok(json!({ "role": role, "parts": parts }));
    }
    // No empty text part beside the calls.
    if m.parts.is_empty() && m.content.is_empty() {
        parts.clear();
    }
    let first = parts.len();
    let mut signed = false;
    for tc in &m.tool_calls {
        let args: Value = if tc.arguments.is_empty() {
            json!({})
        } else {
            serde_json::from_str(&tc.arguments).map_err(|_| {
                invalid(format!(
                    "gemini: tool call {:?} arguments are not valid JSON",
                    tc.id
                ))
            })?
        };
        let sig = thought_signature(tc)?;
        signed |= !sig.is_empty();
        let mut p = json!({ "functionCall": { "name": names.wire(&tc.name), "args": args } });
        if !sig.is_empty() {
            p["thoughtSignature"] = json!(sig);
        }
        parts.push(p);
    }
    // Gemini 3 validates the first call of every current-turn step;
    // earlier turns go out as given.
    if current_turn && !signed {
        parts[first]["thoughtSignature"] = json!(SKIP_THOUGHT_SIGNATURE);
    }
    Ok(json!({ "role": role, "parts": parts }))
}

/// The thought signature `tc` carries from an earlier Gemini turn, or
/// "". Another namespace's data never reaches the wire.
fn thought_signature(tc: &ToolCall) -> Result<String, LlmError> {
    let Some(raw) = tc.provider_data.get(PROVIDER_DATA_KEY) else {
        return Ok(String::new());
    };
    match raw {
        Value::Object(o) => Ok(o
            .get("thought_signature")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_string()),
        _ => Err(invalid(format!(
            "gemini: tool call {:?} provider data: want an object",
            tc.id
        ))),
    }
}

fn signature_data(sig: &str) -> ProviderData {
    let mut d = ProviderData::new();
    if !sig.is_empty() {
        d.insert(
            PROVIDER_DATA_KEY.into(),
            json!({ "thought_signature": sig }),
        );
    }
    d
}

fn function_response_part(
    m: &Message,
    call_names: &BTreeMap<String, String>,
    names: &ToolNames,
) -> Result<Value, LlmError> {
    if m.tool_call_id.is_empty() {
        return Err(invalid("gemini: tool result needs ToolCallID".into()));
    }
    if !m.tool_calls.is_empty() || !m.parts.is_empty() {
        return Err(invalid(format!(
            "gemini: tool result {:?} carries only Content",
            m.tool_call_id
        )));
    }
    let Some(name) = call_names.get(&m.tool_call_id) else {
        return Err(invalid(format!(
            "gemini: tool result {:?} matches no earlier assistant tool call",
            m.tool_call_id
        )));
    };
    // The API wants a struct: a JSON object passes through, anything
    // else is wrapped as {"output": text}.
    let resp = match serde_json::from_str::<Value>(&m.content) {
        Ok(v @ Value::Object(_)) => v,
        _ => json!({ "output": m.content }),
    };
    Ok(json!({ "functionResponse": { "name": names.wire(name), "response": resp } }))
}

fn content_parts(parts: &[Part]) -> Result<Vec<Value>, LlmError> {
    parts
        .iter()
        .map(|p| match p {
            Part::Text(t) => Ok(json!({ "text": t })),
            Part::Media(m) => media(m),
        })
        .collect()
}

fn media(m: &Media) -> Result<Value, LlmError> {
    if m.kind != MediaKind::Image {
        return Err(LlmError::UnsupportedModality {
            modality: m.kind.as_str().into(),
            provider: "gemini".into(),
        });
    }
    let mime = if m.mime_type.is_empty() {
        "image/png"
    } else {
        &m.mime_type
    };
    Ok(json!({ "inlineData": {
        "mimeType": mime,
        "data": base64::engine::general_purpose::STANDARD.encode(&m.data),
    }}))
}

/// `STOP` is `stop`, `MAX_TOKENS` `length`, `SAFETY` `content_filter`;
/// anything else as given.
pub(crate) fn map_finish_reason(reason: &str) -> String {
    match reason {
        "STOP" => "stop",
        "MAX_TOKENS" => "length",
        "SAFETY" => "content_filter",
        other => other,
    }
    .to_string()
}

pub(crate) fn usage_of(u: &Value) -> Usage {
    let n = |k: &str| u[k].as_u64().unwrap_or(0);
    Usage {
        prompt_tokens: n("promptTokenCount"),
        completion_tokens: n("candidatesTokenCount"),
        total_tokens: n("totalTokenCount"),
        ..Usage::default()
    }
}

/// The text parts of a content, joined.
pub(crate) fn content_text(c: &Value) -> String {
    c["parts"]
        .as_array()
        .into_iter()
        .flatten()
        .filter_map(|p| p["text"].as_str())
        .collect()
}

/// A `functionCall` part as a tool call, under the caller's name. A
/// missing id gets a fresh one.
pub(crate) fn call_of(part: &Value, names: &ToolNames) -> ToolCall {
    let fc = &part["functionCall"];
    let id = fc["id"]
        .as_str()
        .filter(|s| !s.is_empty())
        .map(str::to_string)
        .unwrap_or_else(new_call_id);
    ToolCall {
        id,
        name: names
            .original(fc["name"].as_str().unwrap_or_default())
            .to_string(),
        arguments: fc.get("args").map(Value::to_string).unwrap_or_default(),
        provider_data: signature_data(part["thoughtSignature"].as_str().unwrap_or_default()),
    }
}

/// `call_` and 26 base32 characters, as Go's `rand.Text`.
fn new_call_id() -> String {
    use crate::util::retry::{Rng, SplitMix64};
    const ALPHABET: &[u8; 32] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
    let mut rng = SplitMix64::from_entropy();
    let mut out = String::from("call_");
    let (a, b) = (rng.next_u64(), rng.next_u64());
    for i in 0..26 {
        let bits = if i < 13 {
            a >> (5 * i)
        } else {
            b >> (5 * (i - 13))
        };
        out.push(ALPHABET[(bits & 31) as usize] as char);
    }
    out
}

/// A non-streamed `generateContent` response.
pub(crate) struct Parsed {
    pub content: String,
    pub tool_calls: Vec<ToolCall>,
    pub finish_reason: String,
    pub usage: Usage,
}

pub(crate) fn parse(v: &Value, names: &ToolNames) -> Parsed {
    let mut out = Parsed {
        content: String::new(),
        tool_calls: Vec::new(),
        finish_reason: "stop".into(),
        usage: usage_of(&v["usageMetadata"]),
    };
    if let Some(c) = v["candidates"].as_array().and_then(|c| c.first()) {
        out.content = content_text(&c["content"]);
        if let Some(f) = c["finishReason"].as_str().filter(|f| !f.is_empty()) {
            out.finish_reason = map_finish_reason(f);
        }
        for p in c["content"]["parts"].as_array().into_iter().flatten() {
            if p.get("functionCall").is_some_and(|f| !f.is_null()) {
                out.tool_calls.push(call_of(p, names));
            }
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
