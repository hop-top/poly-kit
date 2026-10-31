//! The SSE token stream for Chat Completions, as Go's `streamIter`:
//! text deltas as tokens, tool-call fragments folded by index, the
//! stream finished at `data: [DONE]` or, when the body ends without it,
//! once a choice carried a `finish_reason`; any other end is truncated.
//! Pinned by `contracts/llm-v1` (`openai_stream`, `openai_stream_events`,
//! `openai_accumulate`).

use std::collections::{BTreeMap, VecDeque};

use serde_json::Value;

use crate::llm::wirehttp::sse::{SseDecoder, SseEvent};
use crate::llm::wirehttp::Scrub;
use crate::llm::{
    BoxFuture, LlmError, StreamOutcome, Token, TokenStream, ToolCall, ToolCallEvent,
    ToolCallEventKind, ToolNames, Usage,
};

use super::errors::chunk_error;

/// A tool call as its fragments arrive.
#[derive(Debug, Default)]
struct PartialCall {
    /// The chunk's `tool_calls[].index`.
    wire: i64,
    id: String,
    name: String,
    args: String,
    /// Argument fragments that came before the call started (no id and
    /// name yet).
    pending: String,
    started: bool,
}

/// What a stream needs besides its response.
pub(crate) struct StreamParts {
    pub scheme: String,
    pub model: String,
    pub names: ToolNames,
    /// Tool-call events and usage snapshots stream as tokens.
    pub events: bool,
    pub max_event: u64,
    pub scrub: Scrub,
    pub transport: Box<dyn Fn(reqwest::Error) -> LlmError + Send>,
}

pub(crate) struct ChatStream {
    resp: reqwest::Response,
    decoder: SseDecoder,
    scheme: String,
    model: String,
    names: ToolNames,
    scrub: Scrub,
    transport: Box<dyn Fn(reqwest::Error) -> LlmError + Send>,
    /// Tool-call events and usage snapshots stream as tokens.
    events: bool,
    content: String,
    finish: String,
    usage: Usage,
    /// The turn's calls in order of first fragment; `by_wire` maps a
    /// fragment's index to its position.
    calls: Vec<PartialCall>,
    by_wire: BTreeMap<i64, usize>,
    queue: VecDeque<Token>,
    done: bool,
    outcome: Option<StreamOutcome>,
}

impl ChatStream {
    pub(crate) fn new(resp: reqwest::Response, p: StreamParts) -> Self {
        ChatStream {
            resp,
            decoder: SseDecoder::with_limit(p.max_event),
            scheme: p.scheme,
            model: p.model,
            names: p.names,
            scrub: p.scrub,
            transport: p.transport,
            events: p.events,
            content: String::new(),
            finish: String::new(),
            usage: Usage::default(),
            calls: Vec::new(),
            by_wire: BTreeMap::new(),
            queue: VecDeque::new(),
            done: false,
            outcome: None,
        }
    }

    /// Ends the stream with `e`: no outcome, no end event for an open
    /// call.
    fn fail(&mut self, e: LlmError) -> Result<Option<Token>, LlmError> {
        self.done = true;
        self.queue.clear();
        Err(e)
    }

    /// Ends the stream and folds the outcome.
    fn finished(&mut self) -> Result<Option<Token>, LlmError> {
        self.done = true;
        let calls = (0..self.calls.len()).map(|i| self.call(i)).collect();
        self.outcome = Some(StreamOutcome {
            content: std::mem::take(&mut self.content),
            tool_calls: calls,
            finish_reason: self.finish.clone(),
            usage: self.usage,
            ..StreamOutcome::default()
        });
        Ok(Some(Token {
            done: true,
            ..Token::default()
        }))
    }

    /// Call `i` complete: arguments `{}` when none streamed, the name
    /// the caller defined.
    fn call(&self, i: usize) -> ToolCall {
        let c = &self.calls[i];
        ToolCall {
            id: c.id.clone(),
            name: self.names.original(&c.name).to_string(),
            arguments: if c.args.is_empty() {
                "{}".into()
            } else {
                c.args.clone()
            },
            ..ToolCall::default()
        }
    }

    fn push_event(&mut self, ev: ToolCallEvent) {
        self.queue.push_back(Token {
            tool_call: Some(ev),
            ..Token::default()
        });
    }

    /// Folds one data payload, queueing the tokens it yields: text,
    /// then tool-call events, then usage.
    fn chunk(&mut self, data: &str) -> Result<(), LlmError> {
        let v: Value = serde_json::from_str(data).map_err(|e| {
            LlmError::Protocol(format!("{}: decode stream chunk: {e}", self.scheme))
        })?;
        if v["error"].is_object() {
            return Err(chunk_error(&self.scheme, data, &self.model));
        }
        if let Some(ch) = v["choices"].as_array().and_then(|c| c.first()) {
            let delta = &ch["delta"];
            if let Some(text) = delta["content"].as_str().filter(|t| !t.is_empty()) {
                self.content.push_str(text);
                self.queue.push_back(Token {
                    content: text.to_string(),
                    ..Token::default()
                });
            }
            if self.finish.is_empty() {
                // A finished choice's calls are closed: later fragments
                // are not folded.
                for tc in delta["tool_calls"].as_array().into_iter().flatten() {
                    self.fragment(tc);
                }
            }
            if let Some(fr) = ch["finish_reason"].as_str().filter(|f| !f.is_empty()) {
                if self.finish.is_empty() {
                    self.finish = fr.to_string();
                    self.end_calls();
                }
            }
        }
        if v["usage"].is_object() {
            self.usage = super::wire::usage_of(&v["usage"]);
            if self.events {
                self.queue.push_back(Token {
                    usage: Some(self.usage),
                    ..Token::default()
                });
            }
        }
        Ok(())
    }

    /// Folds one `tool_calls[i]` fragment. A call starts on the fragment
    /// that completes its id and name; argument fragments before that go
    /// out with the start.
    fn fragment(&mut self, tc: &Value) {
        let wire = tc["index"].as_i64().unwrap_or(self.calls.len() as i64);
        let i = *self.by_wire.entry(wire).or_insert_with(|| {
            self.calls.push(PartialCall {
                wire,
                ..PartialCall::default()
            });
            self.calls.len() - 1
        });
        let c = &mut self.calls[i];
        if c.id.is_empty() {
            c.id = tc["id"].as_str().unwrap_or_default().to_string();
        }
        if c.name.is_empty() {
            c.name = tc["function"]["name"]
                .as_str()
                .unwrap_or_default()
                .to_string();
        }
        let frag = tc["function"]["arguments"].as_str().unwrap_or_default();
        c.args.push_str(frag);
        if !self.events {
            return;
        }
        if !c.started {
            c.pending.push_str(frag);
            if !c.id.is_empty() && !c.name.is_empty() {
                self.start(i);
            }
            return;
        }
        if !frag.is_empty() {
            self.push_event(ToolCallEvent {
                kind: ToolCallEventKind::Delta,
                index: i,
                id: String::new(),
                name: String::new(),
                arguments_delta: frag.to_string(),
                call: None,
            });
        }
    }

    fn start(&mut self, i: usize) {
        let c = &mut self.calls[i];
        c.started = true;
        let pending = std::mem::take(&mut c.pending);
        let (id, name) = (c.id.clone(), self.names.original(&c.name).to_string());
        self.push_event(ToolCallEvent {
            kind: ToolCallEventKind::Start,
            index: i,
            id,
            name,
            arguments_delta: String::new(),
            call: None,
        });
        if !pending.is_empty() {
            self.push_event(ToolCallEvent {
                kind: ToolCallEventKind::Delta,
                index: i,
                id: String::new(),
                name: String::new(),
                arguments_delta: pending,
                call: None,
            });
        }
    }

    /// Closes every open call, in index order, when a choice finishes;
    /// a call that never got its id or name starts first.
    fn end_calls(&mut self) {
        if !self.events {
            return;
        }
        let mut order: Vec<usize> = (0..self.calls.len()).collect();
        order.sort_by_key(|&i| self.calls[i].wire);
        for i in order {
            if !self.calls[i].started {
                self.start(i);
            }
            let call = self.call(i);
            self.push_event(ToolCallEvent {
                kind: ToolCallEventKind::End,
                index: i,
                id: call.id.clone(),
                name: call.name.clone(),
                arguments_delta: String::new(),
                call: Some(call),
            });
        }
    }

    /// Handles one SSE event: `Some` when it ends the stream or fails.
    fn event(&mut self, ev: SseEvent) -> Option<Result<Option<Token>, LlmError>> {
        let data = ev.data.trim();
        if data.is_empty() {
            return None;
        }
        if data == "[DONE]" {
            return Some(self.finished());
        }
        let data = data.to_string();
        match self.chunk(&data) {
            Ok(()) => None,
            Err(e) => Some(self.fail(scrubbed(&self.scrub, e))),
        }
    }

    async fn next_token(&mut self) -> Result<Option<Token>, LlmError> {
        loop {
            if let Some(t) = self.queue.pop_front() {
                return Ok(Some(t));
            }
            if self.done {
                return Ok(None);
            }
            while let Some(ev) = self.decoder.pop() {
                if let Some(r) = self.event(ev) {
                    return r;
                }
                if !self.queue.is_empty() {
                    break;
                }
            }
            if !self.queue.is_empty() {
                continue;
            }
            match self.resp.chunk().await {
                Ok(Some(bytes)) => {
                    if let Err(limit) = self.decoder.feed(&bytes) {
                        let e = LlmError::ResponseTooLarge {
                            provider: self.scheme.clone(),
                            what: "event",
                            limit,
                        };
                        return self.fail(e);
                    }
                }
                // Some compatible servers end without [DONE] once a
                // choice finished; any other end is incomplete.
                Ok(None) if !self.finish.is_empty() => return self.finished(),
                Ok(None) => {
                    let e = LlmError::Truncated {
                        provider: self.scheme.clone(),
                    };
                    return self.fail(e);
                }
                Err(e) => {
                    let e = (self.transport)(e);
                    return self.fail(e);
                }
            }
        }
    }
}

/// `e` with every credential value removed from its text.
fn scrubbed(scrub: &Scrub, e: LlmError) -> LlmError {
    match e {
        LlmError::Api {
            provider,
            status,
            error_type,
            message,
        } => LlmError::Api {
            provider,
            status,
            error_type: scrub(&error_type),
            message: scrub(&message),
        },
        LlmError::Auth { provider, message } => LlmError::Auth {
            provider,
            message: scrub(&message),
        },
        LlmError::PermissionDenied { provider, message } => LlmError::PermissionDenied {
            provider,
            message: scrub(&message),
        },
        LlmError::HttpStatus { status, message } => LlmError::HttpStatus {
            status,
            message: scrub(&message),
        },
        LlmError::Protocol(m) => LlmError::Protocol(scrub(&m)),
        other => other,
    }
}

impl TokenStream for ChatStream {
    fn next(&mut self) -> BoxFuture<'_, Result<Option<Token>, LlmError>> {
        Box::pin(self.next_token())
    }

    fn outcome(&self) -> Option<&StreamOutcome> {
        self.outcome.as_ref()
    }
}
