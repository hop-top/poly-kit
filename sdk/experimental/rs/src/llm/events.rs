//! The request and response lifecycle payloads (Go
//! `RequestStartPayload`, `RequestEndPayload`): what a call was, never
//! its content. Pinned by `contracts/llm-v1` (`bus_request_started`,
//! `bus_response_received`).

use std::collections::BTreeMap;

use serde::Serialize;

use super::types::{Message, Part, Request, Response, ThinkingBlock, ToolCall, Usage};

/// Published on `kit.ai.request.started` before each call. It describes
/// the request without its content: no message text, tool names or
/// arguments, extension values or media, so a bus that forwards events
/// (to a network peer, a log file, a database) carries no prompt.
///
/// `request` holds the content only when the client was built with
/// [`Client::with_request_content`](super::Client::with_request_content),
/// as that option's transform returned it.
#[derive(Debug, Clone, PartialEq, Default, Serialize)]
#[non_exhaustive]
pub struct RequestStartPayload {
    /// The call: `complete`, `stream`, `tool_call` or `stream_tools`.
    pub op: String,
    /// [`Request::model`]; empty (omitted) means the adapter's own.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub model: String,
    /// The messages, counted.
    pub messages: usize,
    /// The messages by role.
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    pub roles: BTreeMap<String, usize>,
    /// The content parts of multimodal messages by type.
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    pub parts: BTreeMap<String, usize>,
    /// The UTF-8 length of the messages' text: content, text parts, tool
    /// call arguments and thinking text. Media is not counted.
    pub content_bytes: usize,
    /// The tools offered (`tool_call`, `stream_tools`).
    pub tools: usize,
    pub max_tokens: u32,
    pub has_temperature: bool,
    pub thinking: bool,
    /// The stop sequences, counted.
    pub stop_sequences: usize,
    /// [`Request::extensions`] keys, sorted; never their values.
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub extension_keys: Vec<String>,
    /// The content, set only by `with_request_content`.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub request: Option<Request>,
}

impl RequestStartPayload {
    /// `req` described for a call of kind `op` offering `tools` tools.
    pub fn new(op: &str, req: &Request, tools: usize) -> Self {
        let mut p = RequestStartPayload {
            op: op.to_string(),
            model: req.model.clone(),
            messages: req.messages.len(),
            tools,
            max_tokens: req.max_tokens,
            has_temperature: req.temperature.is_some(),
            thinking: req.thinking.is_some(),
            stop_sequences: req.stop_sequences.len(),
            extension_keys: req.extensions.keys().cloned().collect(),
            ..RequestStartPayload::default()
        };
        for m in &req.messages {
            *p.roles.entry(m.role.as_str().to_string()).or_default() += 1;
            p.content_bytes += message_bytes(m);
            for part in &m.parts {
                let kind = match part {
                    Part::Text(_) => "text",
                    Part::Media(media) => media.kind.as_str(),
                };
                *p.parts.entry(kind.to_string()).or_default() += 1;
            }
        }
        p
    }
}

/// Published on `kit.ai.response.received` after a successful call
/// other than a stream; a stream publishes none. Like
/// [`RequestStartPayload`] it carries no content: `response` is set only
/// by [`Client::with_response_content`](super::Client::with_response_content).
#[derive(Debug, Clone, PartialEq, Default, Serialize)]
#[non_exhaustive]
pub struct RequestEndPayload {
    /// The call, as in [`RequestStartPayload::op`].
    pub op: String,
    /// The call's duration in whole milliseconds; 0 where there is no
    /// clock (wasm32-unknown-unknown).
    pub duration_ms: u64,
    /// The provider's stop reason.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub finish_reason: String,
    pub usage: Usage,
    /// The UTF-8 length of the response's text: content, tool call
    /// arguments and thinking text.
    pub content_bytes: usize,
    /// The calls the model made (`tool_call`).
    pub tool_calls: usize,
    /// The content, set only by `with_response_content`.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub response: Option<Response>,
}

impl RequestEndPayload {
    /// `resp` and the `calls` it made described for a call of kind `op`
    /// that took `duration_ms`.
    pub fn new(op: &str, resp: &Response, calls: &[ToolCall], duration_ms: u64) -> Self {
        RequestEndPayload {
            op: op.to_string(),
            duration_ms,
            finish_reason: resp.finish_reason.clone(),
            usage: resp.usage,
            content_bytes: resp.content.len() + call_bytes(calls) + thinking_bytes(&resp.thinking),
            tool_calls: calls.len(),
            response: None,
        }
    }
}

fn message_bytes(m: &Message) -> usize {
    let parts: usize = m
        .parts
        .iter()
        .map(|p| match p {
            Part::Text(t) => t.len(),
            Part::Media(_) => 0,
        })
        .sum();
    m.content.len() + call_bytes(&m.tool_calls) + thinking_bytes(&m.thinking) + parts
}

fn call_bytes(calls: &[ToolCall]) -> usize {
    calls.iter().map(|c| c.arguments.len()).sum()
}

fn thinking_bytes(tbs: &[ThinkingBlock]) -> usize {
    tbs.iter().map(|t| t.thinking.len()).sum()
}
