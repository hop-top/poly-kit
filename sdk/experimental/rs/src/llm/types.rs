//! Conversation data: messages, content parts, tool calls, requests and
//! responses. Plain data, wasm-safe. Mirrors Go `llm.Message` and
//! friends, with Go's string roles as an enum.

use std::collections::BTreeMap;

use serde::Serialize;

/// Who a [`Message`] is from.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Role {
    System,
    #[default]
    User,
    Assistant,
    /// A tool result answering an assistant's [`ToolCall`].
    Tool,
}

impl Role {
    /// The wire name: `system`, `user`, `assistant` or `tool`.
    pub fn as_str(self) -> &'static str {
        match self {
            Role::System => "system",
            Role::User => "user",
            Role::Assistant => "assistant",
            Role::Tool => "tool",
        }
    }
}

/// The modality of a [`Media`] part (Go `PartType` minus text).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum MediaKind {
    Image,
    Audio,
    Video,
}

impl MediaKind {
    pub fn as_str(self) -> &'static str {
        match self {
            MediaKind::Image => "image",
            MediaKind::Audio => "audio",
            MediaKind::Video => "video",
        }
    }
}

/// Inline media. Go's `MediaSource` readers become bytes the caller has
/// already read. `application/pdf` travels as an image part, as in Go.
#[derive(Clone, PartialEq, Serialize)]
pub struct Media {
    pub kind: MediaKind,
    pub mime_type: String,
    /// The raw bytes; events report their length only.
    #[serde(skip)]
    pub data: Vec<u8>,
}

impl std::fmt::Debug for Media {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Media")
            .field("kind", &self.kind)
            .field("mime_type", &self.mime_type)
            .field("len", &self.data.len())
            .finish()
    }
}

/// One element of a multimodal message.
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(tag = "type", content = "value", rename_all = "lowercase")]
pub enum Part {
    Text(String),
    Media(Media),
}

/// A role and its content.
///
/// Multimodal rule: when `parts` is non-empty it is used and `content`
/// is not. Tool-call linkage, as in Go: an assistant turn lists its
/// calls in `tool_calls`; each result comes back as a [`Role::Tool`]
/// message whose `tool_call_id` names the call it answers. Adapters
/// reject either field on any other role.
#[derive(Debug, Clone, PartialEq, Default, Serialize)]
pub struct Message {
    pub role: Role,
    pub content: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub parts: Vec<Part>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub tool_calls: Vec<ToolCall>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub tool_call_id: String,
    /// A prompt-cache breakpoint after this message: on a system message
    /// its text, otherwise its last content block.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cache_control: Option<CacheControl>,
    /// An assistant turn's thinking blocks, replayed exactly as the
    /// provider returned them; tool use with thinking requires them.
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub thinking: Vec<ThinkingBlock>,
}

/// A prompt-cache breakpoint. `ttl` is "" (the provider default, five
/// minutes on Anthropic), "5m" or "1h".
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize)]
pub struct CacheControl {
    pub ttl: String,
}

/// Extended thinking with a token budget, below the request's
/// `max_tokens`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
pub struct Thinking {
    pub budget_tokens: u32,
}

/// One block of extended thinking. `signature` verifies `thinking`; a
/// redacted block carries only `redacted`, the provider's opaque data.
/// Pass blocks back exactly as received.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize)]
pub struct ThinkingBlock {
    pub thinking: String,
    pub signature: String,
    pub redacted: String,
}

impl Message {
    fn text(role: Role, content: impl Into<String>) -> Self {
        Message {
            role,
            content: content.into(),
            ..Message::default()
        }
    }

    pub fn system(content: impl Into<String>) -> Self {
        Self::text(Role::System, content)
    }

    pub fn user(content: impl Into<String>) -> Self {
        Self::text(Role::User, content)
    }

    pub fn assistant(content: impl Into<String>) -> Self {
        Self::text(Role::Assistant, content)
    }

    /// An assistant turn replaying the calls the model made.
    pub fn assistant_calls(content: impl Into<String>, calls: Vec<ToolCall>) -> Self {
        Message {
            tool_calls: calls,
            ..Self::text(Role::Assistant, content)
        }
    }

    /// A tool's result for the call `call_id`.
    pub fn tool_result(call_id: impl Into<String>, content: impl Into<String>) -> Self {
        Message {
            tool_call_id: call_id.into(),
            ..Self::text(Role::Tool, content)
        }
    }
}

/// Provider-opaque state keyed by adapter namespace, replayed verbatim
/// with the call it came on (Go `ProviderData`). Callers pass it through.
pub type ProviderData = BTreeMap<String, serde_json::Value>;

/// One tool invocation the model made.
#[derive(Debug, Clone, PartialEq, Default, Serialize)]
pub struct ToolCall {
    /// The handle a tool result quotes back in `tool_call_id`.
    pub id: String,
    pub name: String,
    /// The arguments as JSON text; empty means none (`{}`).
    pub arguments: String,
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    pub provider_data: ProviderData,
}

/// A tool the model may call.
#[derive(Debug, Clone, PartialEq, Default, Serialize)]
pub struct ToolDef {
    pub name: String,
    pub description: String,
    /// JSON Schema of the arguments; `None` takes no arguments.
    pub parameters: Option<serde_json::Value>,
    /// A prompt-cache breakpoint after this tool.
    pub cache_control: Option<CacheControl>,
}

impl ToolDef {
    /// A tool taking no arguments.
    pub fn named(name: impl Into<String>) -> Self {
        ToolDef {
            name: name.into(),
            ..ToolDef::default()
        }
    }
}

/// Parameters of one call.
///
/// `temperature` `None` leaves the provider default; `Some(0.0)` is
/// sent. `max_tokens` 0 means the adapter's default, as in Go.
#[derive(Debug, Clone, PartialEq, Default, Serialize)]
pub struct Request {
    pub messages: Vec<Message>,
    /// Overrides the provider's configured model when non-empty.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub model: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub temperature: Option<f64>,
    #[serde(skip_serializing_if = "is_zero")]
    pub max_tokens: u32,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub stop_sequences: Vec<String>,
    /// Enables extended thinking; its deltas stream as [`Token::thinking`].
    #[serde(skip_serializing_if = "Option::is_none")]
    pub thinking: Option<Thinking>,
}

fn is_zero(n: &u32) -> bool {
    *n == 0
}

/// Token consumption.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize)]
pub struct Usage {
    pub prompt_tokens: u64,
    pub completion_tokens: u64,
    /// Input tokens read from the prompt cache, apart from `prompt_tokens`.
    pub cache_read_tokens: u64,
    /// Input tokens written to the prompt cache, apart from `prompt_tokens`.
    pub cache_creation_tokens: u64,
    /// Every input and output token, cache ones included.
    pub total_tokens: u64,
}

impl Usage {
    pub fn new(prompt: u64, completion: u64) -> Self {
        Self::with_cache(prompt, completion, 0, 0)
    }

    pub fn with_cache(prompt: u64, completion: u64, cache_read: u64, cache_creation: u64) -> Self {
        Usage {
            prompt_tokens: prompt,
            completion_tokens: completion,
            cache_read_tokens: cache_read,
            cache_creation_tokens: cache_creation,
            total_tokens: prompt + completion + cache_read + cache_creation,
        }
    }

    /// The cost at `p`, cache reads and writes at their own rates.
    pub fn cost(&self, p: &Price) -> f64 {
        (self.prompt_tokens as f64 * p.input
            + self.completion_tokens as f64 * p.output
            + self.cache_read_tokens as f64 * p.cache_read
            + self.cache_creation_tokens as f64 * p.cache_write)
            / 1e6
    }
}

/// A model's rate per million tokens, by kind.
#[derive(Debug, Clone, Copy, PartialEq, Default)]
pub struct Price {
    pub input: f64,
    pub output: f64,
    pub cache_read: f64,
    pub cache_write: f64,
}

/// The result of a completion.
#[derive(Debug, Clone, PartialEq, Default, Serialize)]
pub struct Response {
    pub content: String,
    pub role: Role,
    pub usage: Usage,
    /// The provider's stop reason (`end_turn`, `tool_use`, ...).
    pub finish_reason: String,
    /// The turn's thinking blocks, to replay unchanged.
    pub thinking: Vec<ThinkingBlock>,
}

/// The result of a tool-calling completion.
#[derive(Debug, Clone, PartialEq, Default, Serialize)]
pub struct ToolResponse {
    pub content: String,
    pub tool_calls: Vec<ToolCall>,
    /// The turn's thinking blocks, to replay unchanged.
    pub thinking: Vec<ThinkingBlock>,
    pub finish_reason: String,
    pub usage: Usage,
}

/// One streaming chunk. The last has `done` set and no content.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct Token {
    pub content: String,
    /// A thinking delta, sent only when the request enabled thinking;
    /// `content` is then empty.
    pub thinking: String,
    pub done: bool,
}

/// What a finished stream folded into: everything [`ToolResponse`] and
/// [`Response`] carry, so a streamed tool round needs no second call.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct StreamOutcome {
    pub content: String,
    /// The turn's thinking blocks in order, to replay unchanged.
    pub thinking: Vec<ThinkingBlock>,
    pub tool_calls: Vec<ToolCall>,
    pub finish_reason: String,
    pub usage: Usage,
}
