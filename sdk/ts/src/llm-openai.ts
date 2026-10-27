/**
 * @packageDocumentation
 *
 * OpenAI Chat Completions adapter — registers, on import, the schemes
 * `openai`, `openrouter`, `xai`, `lmstudio`, `groq`, `together`,
 * `fireworks`, `deepseek` and `mistral`. Implements complete, stream,
 * callWithTools and streamWithTools.
 *
 * URI: `openai://<model>[?params]`; a host form
 * (`openai://host:port/model`) names a self-hosted OpenAI-compatible
 * server, reached at `http://host:port/v1`; a `base_url` param is used
 * as given. `lmstudio` defaults to `http://localhost:1234/v1` and
 * appends `/v1` to any base URL that lacks it. Params: `tool_names`
 * (`sanitize` | `strict`), `max_body_bytes`, `max_event_bytes`. The key
 * resolves as `loadConfig` resolves it (the URI's `api_key`, llm.yaml,
 * `<SCHEME>_API_KEY`, then `LLM_API_KEY`; `lmstudio` sends none unless
 * the URI or llm.yaml gives one); a blank key sends no
 * Authorization header.
 *
 * @example
 * ```ts
 * import "@hop-top/kit/openai";
 * import { createLLM } from "@hop-top/kit/llm";
 *
 * const llm = createLLM("openai://gpt-4.1-mini");
 * ```
 */

import {
  AuthError,
  LLMError,
  ModelError,
  OPENAI_TOOL_NAMES,
  PermissionDeniedError,
  RateLimitError,
  ToolNames,
  checkToolChoice,
  checkToolLinkage,
  register,
  toolNameModeOf,
  type Completer,
  type ContentPart,
  type Message,
  type Provider,
  type Request,
  type ResolvedConfig,
  type Response,
  type StreamOutcome,
  type Streamer,
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
import { checkKeyEndpoint, redactURI } from "./llm-endpoint";
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

/** Schemes this adapter registers. */
export const OPENAI_SCHEMES = [
  "openai", "openrouter", "xai", "lmstudio",
  "groq", "together", "fireworks", "deepseek", "mistral",
] as const;

/** OpenAI's API root; schemes without a default below use it. */
export const OPENAI_DEFAULT_BASE_URL = "https://api.openai.com/v1";

/** Each scheme's default API root. */
export const OPENAI_BASE_URLS: Readonly<Record<string, string>> = Object.freeze({
  openrouter: "https://openrouter.ai/api/v1",
  xai: "https://api.x.ai/v1",
  groq: "https://api.groq.com/openai/v1",
  together: "https://api.together.xyz/v1",
  fireworks: "https://api.fireworks.ai/inference/v1",
  deepseek: "https://api.deepseek.com",
  mistral: "https://api.mistral.ai/v1",
  // LM Studio's local server; name another host or port with a
  // host-form URI or ?base_url=.
  lmstudio: "http://localhost:1234/v1",
});

/** Options for adapters that speak the OpenAI wire on their own scheme. */
export interface OpenAIAdapterOptions {
  /** Prefix of error messages; defaults to the URI scheme. */
  label?: string;
}

type Wire = Record<string, unknown>;

/**
 * OpenAI Chat Completions adapter over fetch, for OpenAI and any
 * compatible server.
 */
export class OpenAIAdapter
  implements Completer, Streamer, ToolCaller, ToolStreamer
{
  readonly model: string;
  readonly baseURL: string;
  readonly scheme: string;
  private readonly label: string;
  private readonly headers: Record<string, string>;
  private readonly secrets: string[];
  private readonly limits: Limits;
  private readonly toolNames: ToolNameMode;

  constructor(cfg: ResolvedConfig, opts: OpenAIAdapterOptions = {}) {
    this.scheme = cfg.uri?.scheme || "openai";
    this.label = opts.label ?? this.scheme;
    this.limits = limitsFrom(this.label, cfg.provider.params);
    try {
      this.toolNames = toolNameModeOf(cfg.provider.params);
    } catch (err) {
      throw new LLMError(`${this.label}: ${(err as Error).message}`);
    }

    let base =
      cfg.provider.baseURL || OPENAI_BASE_URLS[this.scheme] || OPENAI_DEFAULT_BASE_URL;
    if (this.scheme === "lmstudio" || (this.scheme === "openai" && isHostForm(cfg))) {
      base = apiRootV1(base);
    }
    try {
      new URL(base);
    } catch {
      throw new LLMError(
        `${this.label}: invalid base URL: ${JSON.stringify(redactURI(base))}`
      );
    }
    this.baseURL = base;
    this.model = cfg.provider.model || cfg.uri?.model || "";

    // A blank key is no key: no Authorization header rather than
    // "Bearer " with nothing after it.
    const key = cfg.provider.apiKey ?? "";
    checkKeyEndpoint(this.scheme, key, base);
    this.headers = {
      "content-type": "application/json",
      accept: "application/json",
    };
    if (key.trim() !== "") this.headers["authorization"] = `Bearer ${key}`;
    this.secrets = key.trim() !== "" ? [key] : [];
  }

  close(): void {
    // fetch holds no per-adapter resources.
  }

  async complete(req: Request): Promise<Response> {
    const names = this.toolNameMap([]);
    const c = await this.send(await this.buildBody(req, names, []), req);
    const choice = c.choices?.[0];
    return {
      content: choice?.message?.content ?? "",
      role: choice?.message?.role ?? "assistant",
      finishReason: choice?.finish_reason ?? "",
      usage: usageOf(c.usage),
    };
  }

  async callWithTools(req: Request, tools: ToolDef[]): Promise<ToolResponse> {
    const names = this.toolNameMap(tools);
    const body = await this.buildBody(req, names, tools);
    body["tools"] = mapTools(names.defs(tools));
    const c = await this.send(body, req);
    const choice = c.choices?.[0];
    return {
      content: choice?.message?.content ?? "",
      toolCalls: (choice?.message?.tool_calls ?? []).map((tc) => ({
        id: tc.id ?? "",
        name: names.original(tc.function?.name ?? ""),
        arguments: parseArguments(tc.function?.arguments),
      })),
      finishReason: choice?.finish_reason ?? "",
      usage: usageOf(c.usage),
    };
  }

  stream(req: Request): ToolStream {
    return this.openStream(req, undefined);
  }

  streamWithTools(req: Request, tools: ToolDef[]): ToolStream {
    return this.openStream(req, tools);
  }

  // ── private ────────────────────────────────────────────────────

  private toolNameMap(tools: ReadonlyArray<{ name: string }>): ToolNames {
    return ToolNames.build(OPENAI_TOOL_NAMES, this.toolNames, tools);
  }

  private url(): string {
    return joinURL(this.baseURL, "chat/completions");
  }

  private transport(req: Request): TransportOptions {
    const model = req.model || this.model;
    return {
      provider: this.label,
      secrets: this.secrets,
      limits: this.limits,
      mapError: (r) => mapErrorResponse(r, this.label, this.scheme, model),
    };
  }

  private async send(body: Wire, req: Request): Promise<WireCompletion> {
    const resp = await post(this.transport(req), this.url(), this.headers, body);
    const text = await readBody(resp, this.limits.body, this.label).catch((e) => {
      throw scrubError(e as Error, this.secrets);
    });
    try {
      return JSON.parse(text) as WireCompletion;
    } catch {
      throw new LLMError(`${this.label}: response is not valid JSON`);
    }
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
    const body = await this.buildBody(req, names, tools ?? []);
    if (tools) body["tools"] = mapTools(names.defs(tools));
    body["stream"] = true;
    const resp = await post(this.transport(req), this.url(), {
      ...this.headers,
      accept: "text/event-stream",
    }, body);
    const acc = new Accumulator();
    try {
      for await (const ev of sseEvents(resp, this.limits.event, this.label)) {
        if (ev.data === "[DONE]") {
          done(acc.outcome(names));
          yield { content: "", done: true };
          return;
        }
        let chunk: WireChunk;
        try {
          chunk = JSON.parse(ev.data) as WireChunk;
        } catch {
          throw new LLMError(`${this.label}: stream event is not valid JSON`);
        }
        if (chunk.error) {
          const e = chunk.error;
          throw new APIError(
            this.label,
            0,
            e.type ?? "",
            `${this.label}: stream error: ${e.type ?? "error"}: ${e.message ?? ""}`
          );
        }
        const content = acc.add(chunk);
        if (content) yield { content, done: false };
      }
    } catch (err) {
      throw scrubError(err as Error, this.secrets);
    }
    // Some compatible servers end without [DONE] once a choice
    // finished; a stream that ends before any finish is incomplete.
    if (!acc.finished()) throw new StreamTruncatedError(this.label);
    done(acc.outcome(names));
    yield { content: "", done: true };
  }

  /**
   * Maps `req`; `tools` are the call's tools, which `req.toolChoice` is
   * checked against (none for a call without tools).
   */
  private async buildBody(
    req: Request,
    names: ToolNames,
    tools: ReadonlyArray<{ name: string }>
  ): Promise<Wire> {
    try {
      checkToolChoice(req.toolChoice, tools);
    } catch (err) {
      throw new LLMError(`${this.label}: ${(err as Error).message}`);
    }
    const messages: Wire[] = [];
    for (const m of req.messages) {
      messages.push(await mapMessage(m, names, this.label));
    }
    const body: Wire = { model: req.model || this.model, messages };
    if (req.temperature !== undefined) body["temperature"] = req.temperature;
    if (req.maxTokens !== undefined && req.maxTokens > 0) {
      body["max_tokens"] = req.maxTokens;
    }
    if (req.stopSequences?.length) body["stop"] = req.stopSequences;
    const c = req.toolChoice;
    if (c) {
      // any is "required"; a named tool goes by the name sent for it.
      body["tool_choice"] =
        c.mode === "tool"
          ? { type: "function", function: { name: names.wire(c.name ?? "") } }
          : c.mode === "any"
            ? "required"
            : c.mode;
    }
    return body;
  }
}

/**
 * Whether cfg's base URL is the one a host-form URI derives
 * (`http://host:port`), rather than one a `base_url` param, the
 * environment or a caller named outright.
 */
function isHostForm(cfg: ResolvedConfig): boolean {
  if (cfg.uri?.params?.["base_url"] !== undefined) return false;
  return !!cfg.uri?.host && cfg.provider.baseURL === `http://${cfg.uri.host}`;
}

/** `base` ending in `/v1`; one already ending in `/v1` is kept. */
export function apiRootV1(base: string): string {
  const trimmed = base.replace(/\/+$/, "");
  return trimmed.endsWith("/v1") ? base : `${trimmed}/v1`;
}

// ─── Message mapping ─────────────────────────────────────────────────────────

/**
 * Maps one message onto the chat wire: assistant `toolCalls` become
 * `tool_calls`, a role `tool` result carries `tool_call_id`, and
 * `parts` become a user content array. Linkage or parts the wire cannot
 * carry throw instead of being sent as plain text. Tool-call names go
 * out as `names` maps them (llm-v1 tool names).
 */
async function mapMessage(m: Message, names: ToolNames, label: string): Promise<Wire> {
  if (m.role === "tool" && !m.toolCallId) {
    throw new LLMError(`${label}: tool result needs toolCallId`);
  }
  checkToolLinkage(m);

  if (m.role === "tool") {
    return { role: "tool", content: m.content, tool_call_id: m.toolCallId };
  }

  if (m.toolCalls?.length) {
    if (m.parts?.length) {
      throw new LLMError(`${label}: assistant tool calls do not support content parts`);
    }
    const out: Wire = { role: "assistant" };
    if (m.content) out["content"] = m.content;
    out["tool_calls"] = m.toolCalls.map((tc) => {
      if (!tc.id) {
        throw new LLMError(`${label}: tool call ${JSON.stringify(tc.name)} needs an id`);
      }
      return {
        id: tc.id,
        type: "function",
        function: {
          name: names.wire(tc.name),
          arguments: encodeArguments(tc, label),
        },
      };
    });
    return out;
  }

  if (!m.parts?.length) return { role: m.role, content: m.content };

  if (m.role !== "" && m.role !== "user") {
    throw new LLMError(
      `${label}: message role ${JSON.stringify(m.role)} does not support content parts`
    );
  }
  const content: Wire[] = [];
  for (const p of m.parts) content.push(await mapPart(p, label));
  return { role: "user", content };
}

/** Tool-call arguments as the JSON string the wire expects. */
function encodeArguments(tc: ToolCall, label: string): string {
  const args = tc.arguments;
  if (args === undefined || args === null) return "{}";
  if (typeof args !== "string") return JSON.stringify(args);
  try {
    JSON.parse(args);
  } catch {
    throw new LLMError(
      `${label}: tool call ${JSON.stringify(tc.id)} arguments are not valid JSON`
    );
  }
  return args;
}

async function mapPart(p: ContentPart, label: string): Promise<Wire> {
  switch (p.type) {
    case "text":
      return { type: "text", text: p.text ?? "" };

    case "image": {
      const mime = p.mimeType || p.source?.mimeType() || "";
      if (mime === "application/pdf") {
        return { type: "file", file: { file_data: await readBase64(p, label) } };
      }
      const url = p.source?.url();
      if (url) return { type: "image_url", image_url: { url } };
      if (!mime) {
        throw new LLMError(`${label}: image part needs a MIME type or URL`);
      }
      return {
        type: "image_url",
        image_url: { url: `data:${mime};base64,${await readBase64(p, label)}` },
      };
    }

    default:
      throw new LLMError(`${label}: unsupported modality ${JSON.stringify(p.type)}`);
  }
}

async function readBase64(p: ContentPart, label: string): Promise<string> {
  if (!p.source) throw new LLMError(`${label}: nil media source`);
  return Buffer.from(await p.source.read()).toString("base64");
}

function mapTools(defs: ToolDef[]): Wire[] {
  return defs.map((d) => {
    const fn: Wire = { name: d.name };
    if (d.description) fn["description"] = d.description;
    const params = parametersOf(d.parameters);
    if (params) fn["parameters"] = params;
    return { type: "function", function: fn };
  });
}

function parametersOf(params: unknown): Wire | undefined {
  let v = params;
  if (typeof v === "string") {
    try {
      v = JSON.parse(v);
    } catch {
      return undefined;
    }
  }
  if (typeof v !== "object" || v === null || Array.isArray(v)) return undefined;
  return v as Wire;
}

// ─── Response mapping ────────────────────────────────────────────────────────

interface WireToolCall {
  index?: number;
  id?: string;
  function?: { name?: string; arguments?: string };
}

interface WireUsage {
  prompt_tokens?: number;
  completion_tokens?: number;
  total_tokens?: number;
}

interface WireCompletion {
  choices?: {
    message?: { content?: string | null; role?: string; tool_calls?: WireToolCall[] };
    finish_reason?: string | null;
  }[];
  usage?: WireUsage | null;
}

interface WireChunk {
  choices?: {
    delta?: { content?: string | null; tool_calls?: WireToolCall[] };
    finish_reason?: string | null;
  }[];
  usage?: WireUsage | null;
  error?: { type?: string; message?: string };
}

function usageOf(u: WireUsage | null | undefined): Usage {
  return {
    promptTokens: u?.prompt_tokens ?? 0,
    completionTokens: u?.completion_tokens ?? 0,
    totalTokens: u?.total_tokens ?? 0,
  };
}

/** Returned arguments parsed, or the raw text when they are not JSON. */
function parseArguments(raw: string | undefined): unknown {
  if (raw === undefined || raw === "") return {};
  try {
    return JSON.parse(raw);
  } catch {
    return raw;
  }
}

/** Folds stream chunks: text, tool-call deltas by index, finish, usage. */
class Accumulator {
  private text = "";
  private finish = "";
  private usage: WireUsage | undefined;
  private calls = new Map<number, { id: string; name: string; args: string }>();

  /** Folds `c`; returns its text delta. */
  add(c: WireChunk): string {
    if (c.usage) this.usage = c.usage;
    const choice = c.choices?.[0];
    if (!choice) return "";
    if (choice.finish_reason) this.finish = choice.finish_reason;
    for (const d of choice.delta?.tool_calls ?? []) {
      const i = d.index ?? this.calls.size;
      const cur = this.calls.get(i) ?? { id: "", name: "", args: "" };
      if (d.id) cur.id = d.id;
      cur.name += d.function?.name ?? "";
      cur.args += d.function?.arguments ?? "";
      this.calls.set(i, cur);
    }
    const content = choice.delta?.content ?? "";
    this.text += content;
    return content;
  }

  finished(): boolean {
    return this.finish !== "";
  }

  outcome(names: ToolNames): StreamOutcome {
    const calls = [...this.calls.entries()]
      .sort(([a], [b]) => a - b)
      .map(([, c]) => ({
        id: c.id,
        name: names.original(c.name),
        arguments: parseArguments(c.args),
      }));
    return {
      content: this.text,
      thinking: [],
      toolCalls: calls,
      finishReason: this.finish,
      usage: usageOf(this.usage),
    };
  }
}

// ─── Errors ──────────────────────────────────────────────────────────────────

function mapErrorResponse(
  r: ErrorResponse,
  label: string,
  scheme: string,
  model: string
): Error {
  let e: { type?: string; message?: string } = {};
  try {
    e = (JSON.parse(r.body) as { error?: typeof e }).error ?? {};
  } catch {
    // not a JSON error body
  }
  const detail =
    e.message !== undefined ? `${e.type ?? "error"}: ${e.message}` : r.body.slice(0, 2048);
  const message = `${label}: ${r.status} ${detail}`.trim();
  const api = new APIError(scheme, r.status, e.type ?? "", message);
  switch (r.status) {
    case 401:
      return new AuthError(scheme, api);
    case 403:
      return new PermissionDeniedError(scheme, api);
    case 429:
      return new RateLimitError(scheme);
    case 404:
      return new ModelError(model, scheme);
  }
  if (r.status >= 500) return new HTTPStatusError(r.status, message);
  return api;
}

// ─── Registration ────────────────────────────────────────────────────────────

function openaiFactory(cfg: ResolvedConfig): Provider {
  // loadConfig resolved and guarded the key.
  const a = new OpenAIAdapter(cfg);
  return {
    complete: (req) => a.complete(req),
    stream: (req) => a.stream(req),
    callWithTools: (req, tools) => a.callWithTools(req, tools),
    streamWithTools: (req, tools) => a.streamWithTools(req, tools),
    close: () => a.close(),
  };
}

for (const s of OPENAI_SCHEMES) {
  register(s, openaiFactory, {
    endpoints: [OPENAI_BASE_URLS[s] ?? OPENAI_DEFAULT_BASE_URL],
    // LM Studio's local server takes no key: none is resolved for it.
    ...(s === "lmstudio" ? { key: { envVars: [], optional: true } } : {}),
  });
}
