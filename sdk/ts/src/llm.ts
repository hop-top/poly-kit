/**
 * @packageDocumentation
 *
 * LLM client facade — mirrors Go `llm` package interfaces as TS types.
 *
 * Provides URI parsing, config loading, provider registry, Client facade
 * with fallback, and typed errors. Adapter implementations (OpenAI,
 * Anthropic, Ollama) are deferred; register them via {@link register}.
 *
 * @example
 * ```ts
 * import { createLLM, register } from '@hop-top/kit/llm';
 *
 * register('mock', (cfg) => ({ complete: async (r) => ({ content: 'hi', role: 'assistant' }), close() {} }));
 * const client = createLLM('mock://model-1');
 * const resp = await client.complete({ messages: [{ role: 'user', content: 'hello' }] });
 * ```
 */

import { readFile } from "node:fs/promises";
import { extname } from "node:path";

// ─── Data types ──────────────────────────────────────────────────────────────

/** Classifies a content part in a multimodal message. */
export type PartType = "text" | "image" | "audio" | "video";

/**
 * Binary media input for a {@link ContentPart}.
 *
 * Built by {@link inlineSource}, {@link urlSource} or {@link fileSource}.
 */
export interface MediaSource {
  /** Non-empty when the source is URL-backed. */
  url(): string;
  /** Inferred MIME type, or `""`. */
  mimeType(): string;
  /** Reads the media bytes. */
  read(): Promise<Uint8Array>;
}

/**
 * A single typed element within a multimodal message.
 *
 * Rule: if a {@link Message} has non-empty `parts`, they are used
 * instead of its `content`.
 */
export interface ContentPart {
  type: PartType;
  text?: string;
  source?: MediaSource;
  mimeType?: string;
  metadata?: Record<string, unknown>;
}

/**
 * A single role+content pair in a conversation.
 *
 * Multimodal rule: non-empty `parts` take precedence over `content`.
 *
 * Tool-call linkage: an agentic loop replays a tool round as two kinds
 * of message.
 *
 * - Role `"assistant"` with `toolCalls`: the calls the model made,
 *   copied from {@link ToolResponse.toolCalls}. `content` carries any
 *   text the model produced alongside them and may be `""`.
 * - Role `"tool"` with `toolCallId`: one tool's result, in `content`.
 *   `toolCallId` equals the {@link ToolCall.id} it answers. Send one
 *   message per call.
 *
 * `toolCalls` is valid only on role `"assistant"` and `toolCallId` only
 * on role `"tool"`; adapters reject either field on any other role, a
 * tool result carrying `parts`, and linkage the provider cannot express,
 * rather than degrading it to plain text ({@link checkToolLinkage}).
 * Messages that set neither field map exactly as before.
 */
export interface Message {
  role: string;
  content: string;
  parts?: ContentPart[];
  /** Tool invocations of an assistant turn. */
  toolCalls?: ToolCall[];
  /** Links a role `"tool"` result to the {@link ToolCall} it answers. */
  toolCallId?: string;
}

export interface Request {
  messages: Message[];
  model?: string;
  temperature?: number;
  maxTokens?: number;
  stopSequences?: string[];
  extensions?: Record<string, unknown>;
}

export interface Response {
  content: string;
  role: string;
  usage?: Usage;
  finishReason?: string;
}

export interface Usage {
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
}

export interface Token {
  content: string;
  done: boolean;
}

export interface ToolDef {
  name: string;
  description: string;
  parameters: unknown;
}

/**
 * A single tool invocation returned by the model.
 *
 * `id` is the handle a tool result quotes back in
 * {@link Message.toolCallId}. `arguments` is the parsed JSON value, or a
 * JSON-encoded string (Go's `json.RawMessage`); adapters send a string
 * as is and encode anything else.
 */
export interface ToolCall {
  id: string;
  name: string;
  arguments: unknown;
}

export interface ToolResponse {
  content: string;
  toolCalls: ToolCall[];
}

// ─── Core interfaces ─────────────────────────────────────────────────────────

export interface Completer {
  complete(req: Request): Promise<Response>;
}

export interface Streamer {
  stream(req: Request): AsyncIterable<Token>;
}

export interface ToolCaller {
  callWithTools(req: Request, tools: ToolDef[]): Promise<ToolResponse>;
}

export interface Provider
  extends Partial<Completer & Streamer & ToolCaller> {
  close(): void;
}

// ─── URI + Config ────────────────────────────────────────────────────────────

export interface URI {
  scheme: string;
  model: string;
  host?: string;
  params?: Record<string, string>;
}

export interface ProviderConfig {
  apiKey?: string;
  baseURL?: string;
  model: string;
  params?: Record<string, string>;
  extras?: Record<string, unknown>;
}

export interface ResolvedConfig {
  uri: URI;
  provider: ProviderConfig;
  fallbacks: string[];
}

// ─── Errors ──────────────────────────────────────────────────────────────────

export class LLMError extends Error {
  statusCode?: number;
  code?: string;

  constructor(message: string) {
    super(message);
    this.name = "LLMError";
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export class ProviderNotFoundError extends LLMError {
  scheme: string;

  constructor(scheme: string) {
    super(`provider not found: ${scheme}`);
    this.name = "ProviderNotFoundError";
    this.scheme = scheme;
  }
}

export class CapabilityNotSupportedError extends LLMError {
  capability: string;
  provider: string;

  constructor(capability: string, provider: string) {
    super(
      `capability "${capability}" not supported by provider "${provider}"`
    );
    this.name = "CapabilityNotSupportedError";
    this.capability = capability;
    this.provider = provider;
  }
}

export class AuthError extends LLMError {
  provider: string;

  constructor(provider: string) {
    super(`authentication failed for provider: ${provider}`);
    this.name = "AuthError";
    this.provider = provider;
  }
}

export class RateLimitError extends LLMError {
  provider: string;
  retryAfter?: number;

  constructor(provider: string, retryAfter?: number) {
    super(`rate limit exceeded for provider: ${provider}`);
    this.name = "RateLimitError";
    this.provider = provider;
    this.retryAfter = retryAfter;
    this.statusCode = 429;
  }
}

export class ModelError extends LLMError {
  model: string;
  provider: string;

  constructor(model: string, provider: string) {
    super(`model "${model}" not available on provider "${provider}"`);
    this.name = "ModelError";
    this.model = model;
    this.provider = provider;
  }
}

export class FallbackExhaustedError extends LLMError {
  errors: Error[];

  constructor(errors: Error[]) {
    super(
      `all providers exhausted (${errors.length} failures)`
    );
    this.name = "FallbackExhaustedError";
    this.errors = errors;
  }
}

/**
 * Returns true when `err` should trigger a fallback attempt.
 *
 * Fallbackable conditions:
 * - LLMError with statusCode >= 500
 * - RateLimitError (429)
 * - Connection/timeout errors (ECONNREFUSED, ETIMEDOUT, ECONNRESET)
 */
export function isFallbackable(err: Error): boolean {
  if (err instanceof RateLimitError) return true;
  if (err instanceof LLMError) {
    if (err.code && isConnectionError(err.code)) return true;
    if (err.statusCode !== undefined && err.statusCode >= 500) return true;
    return false;
  }
  // Handle NodeJS.ErrnoException (e.g. ECONNREFUSED, ETIMEDOUT)
  const code = (err as { code?: string }).code;
  if (code && isConnectionError(code)) return true;
  return false;
}

const CONNECTION_CODES = new Set([
  "ECONNREFUSED",
  "ETIMEDOUT",
  "ECONNRESET",
  "ENETUNREACH",
  "EHOSTUNREACH",
]);

function isConnectionError(code: string): boolean {
  return CONNECTION_CODES.has(code);
}

// ─── Message rules ───────────────────────────────────────────────────────────

/**
 * Enforces the provider-independent tool-call linkage rules on one
 * message: `toolCalls` only on role `"assistant"`, `toolCallId` only on
 * role `"tool"`, and a tool result carries only `content` (no
 * `toolCalls`, no `parts`). Throws {@link LLMError} otherwise, checking
 * in the Go adapters' order. An empty `toolCalls` array counts as unset.
 *
 * Adapters call it before mapping a message, then add the rules their
 * provider needs (for example a required `toolCallId`).
 */
export function checkToolLinkage(m: Message): void {
  const hasCalls = (m.toolCalls?.length ?? 0) > 0;
  if (m.role === "tool") {
    if (hasCalls || (m.parts?.length ?? 0) > 0) {
      throw new LLMError(
        `tool result "${m.toolCallId ?? ""}" carries only content`
      );
    }
    return;
  }
  if (m.toolCallId) {
    throw new LLMError(
      `toolCallId is valid only on role tool, got "${m.role}"`
    );
  }
  if (hasCalls && m.role !== "assistant") {
    throw new LLMError(
      `toolCalls are valid only on role assistant, got "${m.role}"`
    );
  }
}

// ─── Media sources ───────────────────────────────────────────────────────────

/** In-memory media bytes with a caller-supplied MIME type. */
export function inlineSource(data: Uint8Array, mimeType: string): MediaSource {
  return {
    url: () => "",
    mimeType: () => mimeType,
    read: async () => data,
  };
}

/** URL-backed media; `read()` downloads lazily. */
export function urlSource(url: string): MediaSource {
  return {
    url: () => url,
    mimeType: () => "",
    read: async () => {
      const resp = await fetch(url);
      if (!resp.ok) {
        throw new LLMError(
          `url source: unexpected status ${resp.status} fetching ${url}`
        );
      }
      return new Uint8Array(await resp.arrayBuffer());
    },
  };
}

const MIME_BY_EXT: Record<string, string> = {
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".gif": "image/gif",
  ".webp": "image/webp",
  ".pdf": "application/pdf",
  ".mp3": "audio/mpeg",
  ".wav": "audio/wav",
  ".ogg": "audio/ogg",
  ".mp4": "video/mp4",
  ".webm": "video/webm",
  ".mov": "video/quicktime",
};

/**
 * File on disk, read on `read()`. The MIME type comes from the
 * extension (common image, PDF, audio and video types); unknown
 * extensions give `""`, so set {@link ContentPart.mimeType} instead.
 */
export function fileSource(path: string): MediaSource {
  return {
    url: () => "",
    mimeType: () => MIME_BY_EXT[extname(path).toLowerCase()] ?? "",
    read: async () => new Uint8Array(await readFile(path)),
  };
}

// ─── URI parsing ─────────────────────────────────────────────────────────────

/**
 * Parses an LLM URI string into its components.
 *
 * Format: `scheme://[host[:port]/]model[?params]`
 *
 * When the path portion contains a host:port prefix (detected by the
 * presence of a numeric port), the first segment is treated as host.
 * Otherwise the entire path is the model name.
 */
export function parseURI(raw: string): URI {
  const sep = "://";
  const idx = raw.indexOf(sep);
  if (idx < 0) {
    throw new LLMError(`invalid LLM URI (missing "://"): ${raw}`);
  }

  const scheme = raw.slice(0, idx);
  if (!scheme) {
    throw new LLMError(`invalid LLM URI (empty scheme): ${raw}`);
  }

  let rest = raw.slice(idx + sep.length);
  if (!rest) {
    throw new LLMError(`invalid LLM URI (empty model): ${raw}`);
  }

  // Extract query params
  let params: Record<string, string> | undefined;
  const qIdx = rest.indexOf("?");
  if (qIdx >= 0) {
    const qs = rest.slice(qIdx + 1);
    rest = rest.slice(0, qIdx);
    params = {};
    for (const pair of qs.split("&")) {
      const eqIdx = pair.indexOf("=");
      if (eqIdx > 0) {
        params[pair.slice(0, eqIdx)] = decodeURIComponent(
          pair.slice(eqIdx + 1)
        );
      }
    }
  }

  if (!rest) {
    throw new LLMError(`invalid LLM URI (empty model): ${raw}`);
  }

  // Detect host:port pattern.
  // If first segment matches `word:digits`, treat it as host.
  let host: string | undefined;
  let model: string;

  const hostPortMatch = rest.match(/^([^/]+:\d+)\/(.+)$/);
  if (hostPortMatch) {
    host = hostPortMatch[1];
    model = hostPortMatch[2];
  } else {
    model = rest;
  }

  const uri: URI = { scheme, model };
  if (host) uri.host = host;
  if (params && Object.keys(params).length > 0) uri.params = params;
  return uri;
}

// ─── Config loading ──────────────────────────────────────────────────────────

/**
 * Builds a {@link ResolvedConfig} from a URI string + env vars.
 *
 * When `uriStr` is empty/undefined, falls back to `LLM_PROVIDER`
 * env var as a full default URI. If neither is provided, throws.
 *
 * URI query params `api_key` and `base_url` populate provider
 * config. If `uri.host` is set, it derives a baseURL. Env vars
 * `LLM_API_KEY` and `LLM_BASE_URL` override last.
 *
 * Env vars:
 * - `LLM_PROVIDER` — full default URI when no arg provided
 * - `LLM_API_KEY` — overrides provider API key
 * - `LLM_BASE_URL` — overrides provider base URL
 * - `LLM_FALLBACK` — comma-separated fallback URIs
 */
export function loadConfig(uriStr?: string): ResolvedConfig {
  const effective = uriStr || process.env["LLM_PROVIDER"];
  if (!effective) {
    throw new LLMError(
      "no LLM URI provided and LLM_PROVIDER env var is not set"
    );
  }

  const uri = parseURI(effective);

  const fallbackEnv = process.env["LLM_FALLBACK"];
  const fallbacks = fallbackEnv
    ? fallbackEnv.split(",").map((s) => s.trim()).filter(Boolean)
    : [];

  const provider: ProviderConfig = {
    model: uri.model,
  };
  if (uri.params) provider.params = { ...uri.params };

  // Extract api_key and base_url from URI query params
  if (uri.params?.api_key) {
    provider.apiKey = uri.params.api_key;
  }
  if (uri.params?.base_url) {
    provider.baseURL = uri.params.base_url;
  }

  // Derive baseURL from host if present
  if (uri.host) {
    provider.baseURL = `http://${uri.host}`;
  }

  // Env var overrides apply LAST
  const envApiKey = process.env["LLM_API_KEY"] || undefined;
  const envBaseURL = process.env["LLM_BASE_URL"] || undefined;
  if (envApiKey) provider.apiKey = envApiKey;
  if (envBaseURL) provider.baseURL = envBaseURL;

  return { uri, provider, fallbacks };
}

// ─── Registry ────────────────────────────────────────────────────────────────

export type Factory = (cfg: ResolvedConfig) => Provider;

const registry = new Map<string, Factory>();

/** Register a provider factory for the given URI scheme. */
export function register(scheme: string, factory: Factory): void {
  if (registry.has(scheme)) {
    throw new LLMError(`provider already registered for scheme: ${scheme}`);
  }
  registry.set(scheme, factory);
}

/** Resolve a URI to a Provider instance via the registry. */
export function resolve(uriStr: string): Provider {
  const cfg = loadConfig(uriStr);
  const factory = registry.get(cfg.uri.scheme);
  if (!factory) {
    throw new ProviderNotFoundError(cfg.uri.scheme);
  }
  return factory(cfg);
}

/** Clear the registry (for tests). */
export function clearRegistry(): void {
  registry.clear();
}

// ─── Client ──────────────────────────────────────────────────────────────────

export interface ClientOptions {
  fallback?: string[];
  onRequest?: (req: Request) => void;
  onResponse?: (resp: Response, durationMs: number) => void;
  onError?: (err: Error) => void;
  onFallback?: (from: number, to: number, err: Error) => void;
}

export interface Client {
  complete(req: Request): Promise<Response>;
  stream(req: Request): AsyncIterable<Token>;
  callWithTools(req: Request, tools: ToolDef[]): Promise<ToolResponse>;
  capabilities(): string[];
  provider(): Provider;
  close(): void;
}

/**
 * Create an LLM client with fallback support and event hooks.
 *
 * The returned Client delegates to the provider resolved from `uri`.
 * On fallbackable errors, it tries each fallback URI in order.
 */
export function createLLM(uri: string, opts?: ClientOptions): Client {
  const cfg = loadConfig(uri);

  // Merge fallbacks: opts > env > none
  const fallbackURIs = opts?.fallback ?? cfg.fallbacks;

  // Resolve primary; cache fallback providers for close()
  const primary = resolve(uri);
  const allURIs = [uri, ...fallbackURIs];
  const resolvedProviders = new Map<number, Provider>();
  resolvedProviders.set(0, primary);

  function resolveAt(index: number): Provider {
    let p = resolvedProviders.get(index);
    if (!p) {
      p = resolve(allURIs[index]);
      resolvedProviders.set(index, p);
    }
    return p;
  }

  async function withFallback<T>(
    op: string,
    fn: (p: Provider) => Promise<T>
  ): Promise<T> {
    const errors: Error[] = [];
    for (let i = 0; i < allURIs.length; i++) {
      try {
        const p = resolveAt(i);
        if (
          !(op in p) ||
          typeof (p as unknown as Record<string, unknown>)[op] !== "function"
        ) {
          throw new CapabilityNotSupportedError(
            op,
            parseURI(allURIs[i]).scheme
          );
        }
        return await fn(p);
      } catch (err) {
        const e = err instanceof Error ? err : new Error(String(err));
        errors.push(e);
        opts?.onError?.(e);

        // CapabilityNotSupportedError: skip to next provider
        const canContinue =
          isFallbackable(e) || e instanceof CapabilityNotSupportedError;

        if (!canContinue || i === allURIs.length - 1) {
          if (errors.length > 1) {
            throw new FallbackExhaustedError(errors);
          }
          throw e;
        }
        opts?.onFallback?.(i, i + 1, e);
      }
    }
    throw new FallbackExhaustedError(errors);
  }

  return {
    async complete(request: Request): Promise<Response> {
      opts?.onRequest?.(request);
      const start = Date.now();
      const resp = await withFallback("complete", (p) =>
        p.complete!(request)
      );
      opts?.onResponse?.(resp, Date.now() - start);
      return resp;
    },

    stream(request: Request): AsyncIterable<Token> {
      // Route stream through fallback. On creation or first-token
      // failure with a fallbackable/capability error, try next provider.
      return {
        [Symbol.asyncIterator]() {
          let inner: AsyncIterator<Token> | null = null;
          let initialized = false;

          return {
            async next(): Promise<IteratorResult<Token>> {
              if (!initialized) {
                initialized = true;
                opts?.onRequest?.(request);

                const errors: Error[] = [];
                for (let i = 0; i < allURIs.length; i++) {
                  try {
                    const p = resolveAt(i);
                    if (
                      !("stream" in p) ||
                      typeof p.stream !== "function"
                    ) {
                      throw new CapabilityNotSupportedError(
                        "stream",
                        parseURI(allURIs[i]).scheme
                      );
                    }
                    const iterable = p.stream(request);
                    const iter = iterable[Symbol.asyncIterator]();
                    // Attempt first read to detect immediate failures
                    const first = await iter.next();
                    inner = iter;
                    return first;
                  } catch (err) {
                    const e =
                      err instanceof Error
                        ? err
                        : new Error(String(err));
                    errors.push(e);
                    opts?.onError?.(e);
                    const canContinue =
                      isFallbackable(e) ||
                      e instanceof CapabilityNotSupportedError;
                    if (!canContinue || i === allURIs.length - 1) {
                      if (errors.length > 1)
                        throw new FallbackExhaustedError(errors);
                      throw e;
                    }
                    opts?.onFallback?.(i, i + 1, e);
                  }
                }
                throw new FallbackExhaustedError(errors);
              }
              return inner!.next();
            },
          };
        },
      };
    },

    async callWithTools(
      request: Request,
      tools: ToolDef[]
    ): Promise<ToolResponse> {
      opts?.onRequest?.(request);
      const start = Date.now();
      const resp = await withFallback("callWithTools", (p) =>
        p.callWithTools!(request, tools)
      );
      opts?.onResponse?.(
        { content: resp.content, role: "assistant" },
        Date.now() - start
      );
      return resp;
    },

    capabilities(): string[] {
      const caps: string[] = [];
      if (primary.complete) caps.push("complete");
      if (primary.stream) caps.push("stream");
      if (primary.callWithTools) caps.push("callWithTools");
      return caps;
    },

    provider(): Provider {
      return primary;
    },

    close(): void {
      for (const p of resolvedProviders.values()) {
        p.close();
      }
    },
  };
}
