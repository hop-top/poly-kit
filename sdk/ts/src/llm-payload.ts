/**
 * Metadata-only payloads for the llm lifecycle events
 * `kit.ai.request.started` and `kit.ai.response.received`, as the Go
 * client publishes them. Re-exported from `@hop-top/kit/llm`.
 *
 * The TypeScript client publishes no bus events: its `onRequest` and
 * `onResponse` hooks run in process with the full request and response.
 * A caller that forwards those events (to a bus, a log, a peer) builds
 * the payload here instead of sending the request, so no prompt text,
 * tool name or argument, extension value or media leaves the process.
 */

import type { Message, Request, Response, ThinkingBlock, ToolCall } from "./llm";

/** The call a payload describes. */
export type CallOp = "complete" | "stream" | "tool_call" | "stream_tools";

/**
 * `kit.ai.request.started`: a request described without its content.
 * Keys are the wire spelling; optional ones are absent when empty.
 */
export interface RequestStartedPayload {
  op: CallOp;
  /** {@link Request.model}; absent means the adapter's own. */
  model?: string;
  messages: number;
  /** Messages counted by role. */
  roles?: Record<string, number>;
  /** Content parts of multimodal messages counted by type. */
  parts?: Record<string, number>;
  /**
   * UTF-8 length of the messages' text: content, text parts, tool call
   * arguments and thinking text. Media is not counted.
   */
  content_bytes: number;
  /** Tools offered (`tool_call`, `stream_tools`). */
  tools: number;
  max_tokens: number;
  has_temperature: boolean;
  thinking: boolean;
  stop_sequences: number;
  /** {@link Request.extensions} keys, sorted; never their values. */
  extension_keys?: string[];
}

/** `kit.ai.response.received`: a finished call without its content. */
export interface ResponseReceivedPayload {
  op: CallOp;
  finish_reason?: string;
  usage: {
    prompt_tokens: number;
    completion_tokens: number;
    cache_read_tokens: number;
    cache_creation_tokens: number;
    total_tokens: number;
  };
  /**
   * UTF-8 length of the response's text: content, tool call arguments
   * and thinking text.
   */
  content_bytes: number;
  /** Calls the model made (`tool_call`). */
  tool_calls: number;
  /** Whole milliseconds the call took. */
  duration_ms: number;
}

/**
 * The `kit.ai.request.started` payload for `req` on a call of kind
 * `op` offering `tools` tools.
 */
export function requestStartedPayload(
  op: CallOp,
  req: Request,
  tools: number
): RequestStartedPayload {
  const p: RequestStartedPayload = {
    op,
    messages: req.messages.length,
    content_bytes: 0,
    tools,
    max_tokens: req.maxTokens ?? 0,
    has_temperature: req.temperature !== undefined,
    thinking: req.thinking !== undefined,
    stop_sequences: req.stopSequences?.length ?? 0,
  };
  if (req.model) p.model = req.model;
  for (const m of req.messages) {
    p.roles ??= {};
    p.roles[m.role] = (p.roles[m.role] ?? 0) + 1;
    p.content_bytes += messageBytes(m);
    for (const part of m.parts ?? []) {
      p.parts ??= {};
      p.parts[part.type] = (p.parts[part.type] ?? 0) + 1;
      if (part.type === "text") p.content_bytes += bytes(part.text);
    }
  }
  const keys = Object.keys(req.extensions ?? {}).sort();
  if (keys.length > 0) p.extension_keys = keys;
  return p;
}

/**
 * The `kit.ai.response.received` payload for `resp`, with the calls the
 * model made (`tool_call`), after `durationMs`.
 */
export function responseReceivedPayload(
  op: CallOp,
  resp: Response,
  toolCalls: readonly ToolCall[],
  durationMs: number
): ResponseReceivedPayload {
  const u = resp.usage;
  const p: ResponseReceivedPayload = {
    op,
    usage: {
      prompt_tokens: u?.promptTokens ?? 0,
      completion_tokens: u?.completionTokens ?? 0,
      cache_read_tokens: u?.cacheReadTokens ?? 0,
      cache_creation_tokens: u?.cacheCreationTokens ?? 0,
      total_tokens: u?.totalTokens ?? 0,
    },
    content_bytes:
      bytes(resp.content) + callBytes(toolCalls) + thinkingBytes(resp.thinking),
    tool_calls: toolCalls.length,
    duration_ms: Math.max(0, Math.floor(durationMs)),
  };
  if (resp.finishReason) p.finish_reason = resp.finishReason;
  return p;
}

function messageBytes(m: Message): number {
  return bytes(m.content) + callBytes(m.toolCalls ?? []) + thinkingBytes(m.thinking);
}

function bytes(s: string | undefined): number {
  return s ? Buffer.byteLength(s, "utf8") : 0;
}

/** Arguments as sent: a JSON string as is, anything else encoded. */
function callBytes(calls: readonly ToolCall[]): number {
  let n = 0;
  for (const c of calls) {
    const a = c.arguments;
    if (a === undefined || a === null) continue;
    n += bytes(typeof a === "string" ? a : JSON.stringify(a));
  }
  return n;
}

function thinkingBytes(tbs: readonly ThinkingBlock[] | undefined): number {
  let n = 0;
  for (const tb of tbs ?? []) n += bytes(tb.thinking);
  return n;
}
