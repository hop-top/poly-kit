/**
 * @packageDocumentation
 *
 * Anthropic Messages API adapter — registers the `anthropic` URI scheme
 * on import. Implements complete, stream, callWithTools and
 * streamWithTools, with extended thinking and prompt caching.
 *
 * URI: `anthropic://<model>[?params]`. Params: `tool_names`
 * (`sanitize` | `strict`), `max_body_bytes`, `max_event_bytes`. The key
 * is the URI's `api_key`, `LLM_API_KEY` or `ANTHROPIC_API_KEY`;
 * `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN` and
 * `ANTHROPIC_WORKSPACE_ID` are read from the environment. The token and
 * workspace header go only to a trusted origin (the default endpoint,
 * `ANTHROPIC_BASE_URL`, `LLM_BASE_URL`, loopback); no credential goes
 * over plain http to a remote host.
 *
 * @example
 * ```ts
 * import "@hop-top/kit/anthropic";
 * import { createLLM } from "@hop-top/kit/llm";
 *
 * const llm = createLLM("anthropic://claude-sonnet-4-5");
 * const r = await llm.complete({ messages: [{ role: "user", content: "Hi" }] });
 * ```
 */

import {
  ANTHROPIC_TOOL_NAMES,
  AuthError,
  LLMError,
  ModelError,
  RateLimitError,
  ToolNames,
  register,
  toolNameModeOf,
  type CacheControl,
  type Completer,
  type ContentPart,
  type Message,
  type Provider,
  type Request,
  type ResolvedConfig,
  type Response,
  type StreamOutcome,
  type Streamer,
  type ThinkingBlock,
  type Token,
  type ToolCall,
  type ToolCaller,
  type ToolDef,
  type ToolNameMode,
  type ToolResponse,
  type ToolStream,
  type ToolStreamer,
  type Usage,
} from "./llm";
import {
  checkKeyEndpoint,
  credentialsTrusted,
  redactURI,
} from "./llm-endpoint";
import { APIError, HTTPStatusError, StreamTruncatedError } from "./llm-errors";
import {
  joinURL,
  limitsFrom,
  post,
  readBody,
  scrubError,
  sseEvents,
  type ErrorResponse,
  type Limits,
  type TransportOptions,
} from "./llm-http";
import { withResolvedKey } from "./llm-keys";

const SCHEME = "anthropic";
/** The Messages API's default endpoint. */
export const ANTHROPIC_DEFAULT_BASE_URL = "https://api.anthropic.com";
const API_VERSION = "2023-06-01";
const DEFAULT_MAX_TOKENS = 1024;
/** The API's least thinking budget. */
const MIN_THINKING_BUDGET = 1024;

/** An API error type and the HTTP status it stands for. */
const EVENT_STATUS: Record<string, number> = {
  invalid_request_error: 400,
  authentication_error: 401,
  billing_error: 402,
  permission_error: 403,
  not_found_error: 404,
  request_too_large: 413,
  rate_limit_error: 429,
  api_error: 500,
  timeout_error: 504,
  overloaded_error: 529,
};

type Block = Record<string, unknown>;

/**
 * Anthropic Messages API adapter over fetch. Construct it directly with
 * a resolved config, or import the module and resolve an `anthropic://`
 * URI.
 */
export class AnthropicAdapter
  implements Completer, Streamer, ToolCaller, ToolStreamer
{
  readonly model: string;
  readonly baseURL: string;
  private readonly headers: Record<string, string>;
  private readonly secrets: string[];
  private readonly limits: Limits;
  private readonly toolNames: ToolNameMode;

  constructor(cfg: ResolvedConfig) {
    const key = cfg.provider.apiKey ?? "";
    if (key.trim() === "") throw new LLMError("anthropic: API key required");
    this.limits = limitsFrom(SCHEME, cfg.provider.params);
    try {
      this.toolNames = toolNameModeOf(cfg.provider.params);
    } catch (err) {
      throw new LLMError(`anthropic: ${(err as Error).message}`);
    }

    const envBase = process.env["ANTHROPIC_BASE_URL"];
    if (envBase !== undefined) checkBaseURL("ANTHROPIC_BASE_URL", envBase);
    if (cfg.provider.baseURL) checkBaseURL("base URL", cfg.provider.baseURL);
    const base = cfg.provider.baseURL || envBase || ANTHROPIC_DEFAULT_BASE_URL;
    this.baseURL = base;
    this.model = cfg.provider.model || cfg.uri?.model || "";

    // Every credential passes one gate: none over plain http to a
    // remote host or to a malformed endpoint; the env token and
    // workspace header only to a trusted origin.
    checkKeyEndpoint(SCHEME, key, base);
    this.headers = {
      "content-type": "application/json",
      accept: "application/json",
      "anthropic-version": API_VERSION,
      "x-api-key": key,
    };
    const token = process.env["ANTHROPIC_AUTH_TOKEN"] ?? "";
    const env: Record<string, string> = {};
    if (token.trim() !== "") {
      checkKeyEndpoint(SCHEME, token, base);
      env["authorization"] = `Bearer ${token}`;
    }
    const ws = process.env["ANTHROPIC_WORKSPACE_ID"] ?? "";
    if (ws !== "") env["anthropic-workspace-id"] = ws;
    const trusted = [ANTHROPIC_DEFAULT_BASE_URL];
    if (envBase) trusted.push(envBase);
    if (credentialsTrusted(SCHEME, base, ...trusted)) {
      Object.assign(this.headers, env);
    }
    this.secrets = [key, token].filter((s) => s.trim() !== "");
  }

  close(): void {
    // fetch holds no per-adapter resources.
  }

  async complete(req: Request): Promise<Response> {
    const names = this.toolNameMap([]);
    const msg = await this.send(await this.buildBody(req, names), req);
    return {
      content: textOf(msg),
      role: "assistant",
      finishReason: msg.stop_reason ?? "",
      usage: usageOf(msg.usage),
      thinking: thinkingBlocks(msg),
    };
  }

  async callWithTools(req: Request, tools: ToolDef[]): Promise<ToolResponse> {
    const names = this.toolNameMap(tools);
    const body = await this.buildBody(req, names);
    body["tools"] = mapTools(names.defs(tools));
    const msg = await this.send(body, req);
    return toolResponseOf(msg, names);
  }

  stream(req: Request): ToolStream {
    return this.openStream(req, undefined);
  }

  streamWithTools(req: Request, tools: ToolDef[]): ToolStream {
    return this.openStream(req, tools);
  }

  // ── private ────────────────────────────────────────────────────

  private toolNameMap(tools: ReadonlyArray<{ name: string }>): ToolNames {
    return ToolNames.build(ANTHROPIC_TOOL_NAMES, this.toolNames, tools);
  }

  private transport(req: Request): TransportOptions {
    const model = req.model || this.model;
    return {
      provider: SCHEME,
      secrets: this.secrets,
      limits: this.limits,
      mapError: (r) => mapErrorResponse(r, model),
    };
  }

  private async send(body: Block, req: Request): Promise<WireMessage> {
    const opts = this.transport(req);
    const resp = await post(opts, this.url(), this.headers, body);
    const text = await readBody(resp, this.limits.body, SCHEME).catch((e) => {
      throw scrubError(e as Error, this.secrets);
    });
    try {
      return JSON.parse(text) as WireMessage;
    } catch {
      throw new LLMError("anthropic: response is not valid JSON");
    }
  }

  private url(): string {
    return joinURL(this.baseURL, "v1/messages");
  }

  private openStream(req: Request, tools: ToolDef[] | undefined): ToolStream {
    let outcome: StreamOutcome | undefined;
    const gen = this.run(req, tools, (o) => {
      outcome = o;
    });
    return {
      [Symbol.asyncIterator]: () => gen,
      outcome: () => outcome,
    };
  }

  private async *run(
    req: Request,
    tools: ToolDef[] | undefined,
    done: (o: StreamOutcome) => void
  ): AsyncGenerator<Token> {
    const names = this.toolNameMap(tools ?? []);
    const body = await this.buildBody(req, names);
    if (tools) body["tools"] = mapTools(names.defs(tools));
    body["stream"] = true;
    const opts = this.transport(req);
    const resp = await post(opts, this.url(), {
      ...this.headers,
      accept: "text/event-stream",
    }, body);
    const acc = new Accumulator();
    try {
      for await (const ev of sseEvents(resp, this.limits.event, SCHEME)) {
        let evt: StreamEvent;
        try {
          evt = JSON.parse(ev.data) as StreamEvent;
        } catch {
          throw new LLMError("anthropic: stream event is not valid JSON");
        }
        if (evt.type === "error") {
          throw streamError(evt.error, req.model || this.model);
        }
        acc.add(evt);
        if (evt.type === "content_block_delta") {
          const d = evt.delta ?? {};
          if (d.type === "text_delta") {
            yield { content: d.text ?? "", done: false };
          } else if (d.type === "thinking_delta" && req.thinking) {
            yield { content: "", thinking: d.thinking ?? "", done: false };
          }
        } else if (evt.type === "message_stop") {
          done(acc.outcome(names));
          yield { content: "", done: true };
          return;
        }
      }
    } catch (err) {
      throw scrubError(err as Error, this.secrets);
    }
    // No message_stop: the response is incomplete.
    throw new StreamTruncatedError(SCHEME);
  }

  private async buildBody(req: Request, names: ToolNames): Promise<Block> {
    const maxTokens =
      req.maxTokens !== undefined && req.maxTokens > 0
        ? req.maxTokens
        : DEFAULT_MAX_TOKENS;
    const { system, systemCC, messages } = await extractSystem(
      req.messages,
      names
    );
    const body: Block = {
      model: req.model || this.model,
      max_tokens: maxTokens,
      messages,
    };
    if (system !== "") {
      const sp: Block = { type: "text", text: system };
      const cc = cacheParam(systemCC);
      if (cc) sp["cache_control"] = cc;
      body["system"] = [sp];
    }
    if (req.thinking) {
      const b = req.thinking.budgetTokens;
      if (b < MIN_THINKING_BUDGET) {
        throw new LLMError(
          `anthropic: thinking budget_tokens must be at least ${MIN_THINKING_BUDGET}, got ${b}`
        );
      }
      if (b >= maxTokens) {
        throw new LLMError(
          `anthropic: thinking budget_tokens (${b}) must be less than max_tokens (${maxTokens})`
        );
      }
      body["thinking"] = { type: "enabled", budget_tokens: b };
    }
    if (req.temperature !== undefined) body["temperature"] = req.temperature;
    if (req.stopSequences?.length) body["stop_sequences"] = req.stopSequences;
    return body;
  }
}

function checkBaseURL(what: string, base: string): void {
  try {
    new URL(base);
  } catch {
    throw new LLMError(`anthropic: invalid ${what}: ${JSON.stringify(redactURI(base))}`);
  }
}

// ─── Request mapping ─────────────────────────────────────────────────────────

/**
 * Separates any system message from the conversation (the last one
 * wins); the API takes system as a top-level field.
 */
async function extractSystem(
  msgs: Message[],
  names: ToolNames
): Promise<{ system: string; systemCC?: CacheControl; messages: Block[] }> {
  let system = "";
  let systemCC: CacheControl | undefined;
  const out: { role: string; content: Block[] }[] = [];

  for (let i = 0; i < msgs.length; i++) {
    const m = msgs[i]!;
    if (m.role === "system") {
      system = m.content;
      systemCC = m.cacheControl;
      continue;
    }
    checkLinkageRole(m);

    if (m.role === "tool") {
      const block = toolResultBlock(m);
      setCache(block, m.cacheControl);
      // Consecutive results answer one assistant turn and belong in a
      // single user message.
      if (i > 0 && msgs[i - 1]!.role === "tool" && out.length > 0) {
        out[out.length - 1]!.content.push(block);
        continue;
      }
      out.push({ role: "user", content: [block] });
      continue;
    }

    let blocks: Block[] = [];
    const calls = m.toolCalls ?? [];
    if (m.parts?.length) {
      blocks = await mapParts(m.parts);
    } else if (calls.length === 0 || m.content !== "") {
      blocks.push({ type: "text", text: m.content });
    }
    blocks.push(...toolUseBlocks(calls, names));
    if (blocks.length > 0) setCache(blocks[blocks.length - 1]!, m.cacheControl);
    // Thinking blocks lead the turn, unchanged and in order.
    blocks = [...replayThinking(m.thinking ?? []), ...blocks];
    out.push({ role: m.role, content: blocks });
  }
  return { system, systemCC, messages: out };
}

/**
 * Rejects tool-call linkage on a role that cannot carry it: toolCalls
 * and thinking belong to assistant turns, toolCallId to tool results.
 */
function checkLinkageRole(m: Message): void {
  if ((m.toolCalls?.length ?? 0) > 0 && m.role !== "assistant") {
    throw new LLMError(
      `anthropic: ToolCalls are valid only on role assistant, got ${JSON.stringify(m.role)}`
    );
  }
  if ((m.thinking?.length ?? 0) > 0 && m.role !== "assistant") {
    throw new LLMError(
      `anthropic: Thinking is valid only on role assistant, got ${JSON.stringify(m.role)}`
    );
  }
  if (m.toolCallId && m.role !== "tool") {
    throw new LLMError(
      `anthropic: ToolCallID is valid only on role tool, got ${JSON.stringify(m.role)}`
    );
  }
}

function toolResultBlock(m: Message): Block {
  if (!m.toolCallId) {
    throw new LLMError("anthropic: tool result needs ToolCallID");
  }
  if (m.parts?.length) {
    throw new LLMError(
      `anthropic: tool result ${JSON.stringify(m.toolCallId)} carries only Content`
    );
  }
  const res: Block = { type: "tool_result", tool_use_id: m.toolCallId };
  // The API rejects empty text blocks, so an empty result sends none.
  if (m.content) res["content"] = [{ type: "text", text: m.content }];
  return res;
}

/** An assistant turn's calls as tool_use blocks, under their wire names. */
function toolUseBlocks(calls: ToolCall[], names: ToolNames): Block[] {
  return calls.map((tc) => {
    if (!tc.id) {
      throw new LLMError(`anthropic: tool call ${JSON.stringify(tc.name)} needs an ID`);
    }
    return {
      type: "tool_use",
      id: tc.id,
      name: names.wire(tc.name),
      input: inputOf(tc),
    };
  });
}

/** A call's arguments as a JSON value: a string must parse; none is `{}`. */
function inputOf(tc: ToolCall): unknown {
  const args = tc.arguments;
  if (args === undefined || args === null || args === "") return {};
  if (typeof args !== "string") return args;
  try {
    return JSON.parse(args);
  } catch {
    throw new LLMError(
      `anthropic: tool call ${JSON.stringify(tc.id)} arguments are not valid JSON`
    );
  }
}

function replayThinking(tbs: ThinkingBlock[]): Block[] {
  return tbs.map((tb) =>
    tb.redacted
      ? { type: "redacted_thinking", data: tb.redacted }
      : { type: "thinking", thinking: tb.thinking, signature: tb.signature }
  );
}

/** A cache breakpoint as the wire takes it; undefined for none. */
function cacheParam(cc: CacheControl | undefined): Block | undefined {
  if (!cc) return undefined;
  const ttl = cc.ttl ?? "";
  if (ttl === "") return { type: "ephemeral" };
  if (ttl === "5m" || ttl === "1h") return { type: "ephemeral", ttl };
  throw new LLMError(
    `anthropic: cache_control ttl must be "5m" or "1h", got ${JSON.stringify(ttl)}`
  );
}

function setCache(block: Block, cc: CacheControl | undefined): void {
  const p = cacheParam(cc);
  if (p) block["cache_control"] = p;
}

async function mapParts(parts: ContentPart[]): Promise<Block[]> {
  const out: Block[] = [];
  for (const p of parts) {
    switch (p.type) {
      case "text":
        out.push({ type: "text", text: p.text ?? "" });
        break;
      case "image": {
        const mime = p.mimeType || p.source?.mimeType() || "";
        if (!mime) throw new LLMError("anthropic: image part needs a MIME type");
        if (!p.source) throw new LLMError("anthropic: nil media source");
        const data = Buffer.from(await p.source.read()).toString("base64");
        out.push(
          mime === "application/pdf"
            ? { type: "document", source: { type: "base64", media_type: mime, data } }
            : { type: "image", source: { type: "base64", media_type: mime, data } }
        );
        break;
      }
      default:
        throw new LLMError(`anthropic: unsupported modality ${JSON.stringify(p.type)}`);
    }
  }
  return out;
}

/**
 * Tool definitions as the API takes them. `input_schema` is required: a
 * tool without parameters, or with ones that are not a JSON object,
 * takes an empty object schema.
 */
function mapTools(defs: ToolDef[]): Block[] {
  return defs.map((d) => {
    const t: Block = {
      name: d.name,
      description: d.description ?? "",
      input_schema: schemaOf(d.parameters),
    };
    const cc = cacheParam(d.cacheControl);
    if (cc) t["cache_control"] = cc;
    return t;
  });
}

function schemaOf(params: unknown): Block {
  let v = params;
  if (typeof v === "string") {
    try {
      v = JSON.parse(v);
    } catch {
      v = undefined;
    }
  }
  if (typeof v !== "object" || v === null || Array.isArray(v)) {
    return { type: "object", properties: {} };
  }
  return { type: "object", ...(v as Block) };
}

// ─── Response mapping ────────────────────────────────────────────────────────

interface WireBlock {
  type: string;
  text?: string;
  id?: string;
  name?: string;
  input?: unknown;
  thinking?: string;
  signature?: string;
  data?: string;
}

interface WireUsage {
  input_tokens?: number;
  output_tokens?: number;
  cache_read_input_tokens?: number | null;
  cache_creation_input_tokens?: number | null;
}

interface WireMessage {
  content?: WireBlock[];
  stop_reason?: string | null;
  usage?: WireUsage;
}

function textOf(msg: WireMessage): string {
  return (msg.content ?? [])
    .filter((b) => b.type === "text")
    .map((b) => b.text ?? "")
    .join("");
}

function usageOf(u: WireUsage | undefined): Usage {
  const out = {
    promptTokens: u?.input_tokens ?? 0,
    completionTokens: u?.output_tokens ?? 0,
    cacheReadTokens: u?.cache_read_input_tokens ?? 0,
    cacheCreationTokens: u?.cache_creation_input_tokens ?? 0,
    totalTokens: 0,
  };
  out.totalTokens =
    out.promptTokens +
    out.completionTokens +
    out.cacheReadTokens +
    out.cacheCreationTokens;
  return out;
}

function thinkingBlocks(msg: WireMessage): ThinkingBlock[] {
  const out: ThinkingBlock[] = [];
  for (const b of msg.content ?? []) {
    if (b.type === "thinking") {
      out.push({ thinking: b.thinking ?? "", signature: b.signature ?? "", redacted: "" });
    } else if (b.type === "redacted_thinking") {
      out.push({ thinking: "", signature: "", redacted: b.data ?? "" });
    }
  }
  return out;
}

/** Maps `msg`; each call carries the name the caller defined. */
function toolResponseOf(msg: WireMessage, names: ToolNames): ToolResponse {
  const calls: ToolCall[] = [];
  for (const b of msg.content ?? []) {
    if (b.type === "tool_use") {
      calls.push({
        id: b.id ?? "",
        name: names.original(b.name ?? ""),
        arguments: b.input ?? {},
      });
    }
  }
  return {
    content: textOf(msg),
    toolCalls: calls,
    thinking: thinkingBlocks(msg),
    finishReason: msg.stop_reason ?? "",
    usage: usageOf(msg.usage),
  };
}

// ─── Streaming ───────────────────────────────────────────────────────────────

interface StreamEvent {
  type: string;
  index?: number;
  message?: WireMessage;
  content_block?: WireBlock;
  delta?: {
    type?: string;
    text?: string;
    thinking?: string;
    signature?: string;
    partial_json?: string;
    stop_reason?: string | null;
  };
  usage?: WireUsage;
  error?: { type?: string; message?: string };
}

/** A block being streamed; tool input accumulates as raw JSON. */
interface AccBlock extends WireBlock {
  inputRaw?: string;
}

/**
 * Folds stream events into the finished message, as the Go SDK's
 * Message.Accumulate does: blocks start in index order, deltas address
 * a started block, usage totals overwrite.
 */
class Accumulator {
  private content: AccBlock[] = [];
  private stopReason = "";
  private usage: WireUsage = {};

  add(evt: StreamEvent): void {
    switch (evt.type) {
      case "message_start": {
        const m = evt.message ?? {};
        this.content = (m.content ?? []).map((b) => ({ ...b }));
        this.stopReason = m.stop_reason ?? "";
        this.usage = { ...(m.usage ?? {}) };
        break;
      }
      case "message_delta": {
        this.stopReason = evt.delta?.stop_reason ?? "";
        const u = evt.usage ?? {};
        this.usage.output_tokens = u.output_tokens ?? 0;
        if (u.input_tokens !== undefined) this.usage.input_tokens = u.input_tokens;
        if (u.cache_read_input_tokens != null) {
          this.usage.cache_read_input_tokens = u.cache_read_input_tokens;
        }
        if (u.cache_creation_input_tokens != null) {
          this.usage.cache_creation_input_tokens = u.cache_creation_input_tokens;
        }
        break;
      }
      case "content_block_start": {
        if (evt.index !== this.content.length) {
          throw new LLMError(
            `anthropic: received event of type content_block_start for content block at index ${evt.index}, expected index ${this.content.length}`
          );
        }
        const b: AccBlock = { ...(evt.content_block ?? { type: "" }) };
        if (b.type === "tool_use") b.inputRaw = JSON.stringify(b.input ?? {});
        this.content.push(b);
        break;
      }
      case "content_block_delta": {
        const i = evt.index ?? -1;
        if (i < 0 || i >= this.content.length) {
          throw new LLMError(
            `anthropic: received event of type content_block_delta for content block at index ${i}, only ${this.content.length} blocks exist`
          );
        }
        const b = this.content[i]!;
        const d = evt.delta ?? {};
        switch (d.type) {
          case "text_delta":
            b.text = (b.text ?? "") + (d.text ?? "");
            break;
          case "input_json_delta":
            if (d.partial_json) {
              b.inputRaw =
                b.inputRaw === "{}" || b.inputRaw === undefined
                  ? d.partial_json
                  : b.inputRaw + d.partial_json;
            }
            break;
          case "thinking_delta":
            b.thinking = (b.thinking ?? "") + (d.thinking ?? "");
            break;
          case "signature_delta":
            b.signature = (b.signature ?? "") + (d.signature ?? "");
            break;
        }
        break;
      }
    }
  }

  outcome(names: ToolNames): StreamOutcome {
    const msg: WireMessage = {
      content: this.content.map((b) =>
        b.type === "tool_use" ? { ...b, input: parseInput(b.inputRaw) } : b
      ),
      stop_reason: this.stopReason,
      usage: this.usage,
    };
    const tr = toolResponseOf(msg, names);
    return {
      content: tr.content,
      thinking: tr.thinking ?? [],
      toolCalls: tr.toolCalls,
      finishReason: tr.finishReason ?? "",
      usage: tr.usage!,
    };
  }
}

/** Streamed tool input: parsed JSON, or the raw text when it is not JSON. */
function parseInput(raw: string | undefined): unknown {
  if (raw === undefined || raw === "") return {};
  try {
    return JSON.parse(raw);
  } catch {
    return raw;
  }
}

// ─── Errors ──────────────────────────────────────────────────────────────────

interface WireError {
  type?: string;
  message?: string;
}

function mapErrorResponse(r: ErrorResponse, model: string): Error {
  let e: WireError = {};
  try {
    e = (JSON.parse(r.body) as { error?: WireError }).error ?? {};
  } catch {
    // not a JSON error body
  }
  const detail = e.message !== undefined
    ? `${e.type ?? "error"}: ${e.message}`
    : r.body.slice(0, 2048);
  return classify(r.status, e.type ?? "", `anthropic: ${r.status} ${detail}`.trim(), model);
}

/**
 * An SSE error event arrives on a 200 response: classify it by the
 * status its error type stands for (overloaded_error is a 529).
 */
function streamError(e: WireError | undefined, model: string): Error {
  const type = e?.type ?? "";
  const status = EVENT_STATUS[type] ?? 0;
  return classify(status, type, `anthropic: stream error: ${type}: ${e?.message ?? ""}`, model);
}

function classify(status: number, type: string, message: string, model: string): Error {
  const api = new APIError(SCHEME, status, type, message);
  switch (status) {
    case 401:
    case 403:
      return new AuthError(SCHEME, api);
    case 429:
      return new RateLimitError(SCHEME);
    case 404:
      return new ModelError(model, SCHEME);
  }
  if (status >= 500) return new HTTPStatusError(status, message);
  return api;
}

// ─── Registration ────────────────────────────────────────────────────────────

function anthropicFactory(cfg: ResolvedConfig): Provider {
  const envBase = process.env["ANTHROPIC_BASE_URL"];
  const trusted = [ANTHROPIC_DEFAULT_BASE_URL, ...(envBase ? [envBase] : [])];
  const a = new AnthropicAdapter(
    withResolvedKey(cfg, "ANTHROPIC_API_KEY", trusted)
  );
  return {
    complete: (req) => a.complete(req),
    stream: (req) => a.stream(req),
    callWithTools: (req, tools) => a.callWithTools(req, tools),
    streamWithTools: (req, tools) => a.streamWithTools(req, tools),
    close: () => a.close(),
  };
}

register(SCHEME, anthropicFactory, { endpoints: [ANTHROPIC_DEFAULT_BASE_URL] });
