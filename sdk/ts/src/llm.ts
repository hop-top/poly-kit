/**
 * @packageDocumentation
 *
 * LLM client facade — mirrors Go `llm` package interfaces as TS types.
 *
 * Provides URI parsing, config loading, provider registry, Client facade
 * with fallback, and typed errors. Adapters register their URI schemes
 * on import: `@hop-top/kit/anthropic` (`anthropic://`),
 * `@hop-top/kit/openai` (`openai://` and OpenAI-compatible schemes)
 * and `@hop-top/kit/routellm`. Register your own via {@link register}.
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
import { LLMError } from "./llm-error";
import {
  APIError,
  HTTPStatusError,
  KeyEndpointError,
  MissingKeyError,
  ResponseTooLargeError,
  StreamTruncatedError,
} from "./llm-errors";
import { ToolNameError } from "./llm-toolnames";
import {
  DEFAULT_MAX_MEDIA_BYTES,
  MediaTooLargeError,
  MediaURLRefusedError,
  fetchMedia,
  isPublicAddress,
} from "./llm-media";

export * from "./llm-toolnames";
export { DEFAULT_MAX_MEDIA_BYTES, MediaTooLargeError, MediaURLRefusedError, isPublicAddress };
export {
  APIError,
  HTTPStatusError,
  KeyEndpointError,
  MissingKeyError,
  ResponseTooLargeError,
  StreamTruncatedError,
};
import {
  clearDeclarations,
  declarationOf,
  setDeclaration,
  type Declaration,
} from "./llm-endpoint";
import { loadConfig, type LoadOptions } from "./llm-config";
import { parseURI } from "./llm-uri";
export {
  checkKeyEndpoint,
  credentialsTrusted,
  redactURI,
  type ProviderKey,
} from "./llm-endpoint";
export { loadConfig, parseURI, type LoadOptions };
export { applyAPIKey } from "./llm-keys";
export {
  requestStartedPayload,
  responseReceivedPayload,
  type CallOp,
  type RequestStartedPayload,
  type ResponseReceivedPayload,
} from "./llm-payload";

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
  /**
   * Replays an assistant turn's thinking blocks, exactly as the provider
   * returned them ({@link Response.thinking}, {@link ToolResponse.thinking}
   * or {@link StreamOutcome.thinking}). Required with tool use when
   * thinking is enabled. Assistant only.
   */
  thinking?: ThinkingBlock[];
  /**
   * Marks a prompt-cache breakpoint after this message: on a system
   * message its text, otherwise its last content block.
   */
  cacheControl?: CacheControl;
}

/**
 * One block of a model's extended thinking. `signature` verifies
 * `thinking`; a redacted block carries only `redacted`, the provider's
 * opaque data. Pass blocks back exactly as received.
 */
export interface ThinkingBlock {
  thinking: string;
  signature: string;
  redacted: string;
}

/**
 * A prompt-cache breakpoint. `ttl` is `""` or absent (the provider
 * default, five minutes on Anthropic), `"5m"` or `"1h"`.
 */
export interface CacheControl {
  ttl?: string;
}

/**
 * Enables extended thinking with a token budget, which must be below
 * the request's max tokens.
 */
export interface ThinkingConfig {
  budgetTokens: number;
}

export interface Request {
  messages: Message[];
  model?: string;
  temperature?: number;
  maxTokens?: number;
  stopSequences?: string[];
  extensions?: Record<string, unknown>;
  /** Enables extended thinking; thinking deltas then stream as {@link Token.thinking}. */
  thinking?: ThinkingConfig;
  /**
   * Makes a stream also yield incremental tool-call events
   * ({@link Token.toolCall}) and usage snapshots ({@link Token.usage}).
   * Unset, a stream yields exactly the tokens it always has. Ignored by
   * `complete` and `callWithTools`.
   */
  streamEvents?: boolean;
  /**
   * Constrains tool use on a tool-calling request (`callWithTools`,
   * `streamWithTools`); unset leaves the provider default. See
   * {@link ToolChoice}.
   */
  toolChoice?: ToolChoice;
}

/** Whether the model must call a tool. */
export type ToolChoiceMode = "auto" | "any" | "none" | "tool";

/**
 * Constrains tool use: `auto` lets the model decide (the providers'
 * default), `any` makes it call at least one tool of its pick, `none`
 * forbids tool calls although tools are defined, `tool` makes it call
 * the tool `name`: its {@link ToolDef.name} as defined, before any
 * provider name mapping. Set `name` with mode `tool` only.
 *
 * Adapters reject an invalid choice ({@link checkToolChoice}) before
 * sending, and so does a call without tools (`complete`, `stream`).
 */
export interface ToolChoice {
  mode: ToolChoiceMode;
  name?: string;
}

const TOOL_CHOICE_MODES: readonly string[] = ["auto", "any", "none", "tool"];

/**
 * Throws an {@link LLMError} saying why `c` cannot go with `tools`: a
 * mode that is not a {@link ToolChoiceMode}, no tools, a name missing
 * with mode `tool` or set with another mode, or naming no tool in
 * `tools`. Undefined `c` is valid. Adapters prefix the message with
 * their scheme.
 */
export function checkToolChoice(
  c: ToolChoice | undefined,
  tools: ReadonlyArray<{ name: string }>
): void {
  if (!c) return;
  const q = (v: string) => JSON.stringify(v);
  if (!TOOL_CHOICE_MODES.includes(c.mode)) {
    throw new LLMError(`unknown tool_choice mode ${q(String(c.mode))}`);
  }
  if (tools.length === 0) {
    throw new LLMError(`tool_choice ${q(c.mode)} needs tools`);
  }
  const name = c.name ?? "";
  if (c.mode !== "tool") {
    if (name !== "") {
      throw new LLMError(`tool_choice ${q(c.mode)} takes no tool name`);
    }
    return;
  }
  if (name === "") {
    throw new LLMError(`tool_choice ${q(c.mode)} needs a tool name`);
  }
  if (!tools.some((t) => t.name === name)) {
    throw new LLMError(`tool_choice names unknown tool ${q(name)}`);
  }
}

export interface Response {
  content: string;
  role: string;
  usage?: Usage;
  finishReason?: string;
  /** The turn's thinking blocks, to replay unchanged. */
  thinking?: ThinkingBlock[];
}

export interface Usage {
  promptTokens: number;
  completionTokens: number;
  /**
   * Input tokens read from or written to the prompt cache, apart from
   * `promptTokens`.
   */
  cacheReadTokens?: number;
  cacheCreationTokens?: number;
  /** Every input and output token, cache ones included. */
  totalTokens: number;
}

/** A model's rate per million tokens, by kind. */
export interface Price {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
}

/** Cost of `u` at `p`, cache reads and writes at their own rates. */
export function usageCost(u: Usage, p: Price): number {
  return (
    (u.promptTokens * p.input +
      u.completionTokens * p.output +
      (u.cacheReadTokens ?? 0) * p.cacheRead +
      (u.cacheCreationTokens ?? 0) * p.cacheWrite) /
    1e6
  );
}

/**
 * A single streaming chunk. The final token has `done: true`; nothing
 * follows it. A token carries one thing: a text delta (`content`), a
 * thinking delta (`thinking`), a tool-call event (`toolCall`), a usage
 * snapshot (`usage`), or the end (`done`); `content` is `""` on all but
 * a text delta.
 */
export interface Token {
  content: string;
  /**
   * A thinking delta, sent only when the request enabled
   * {@link Request.thinking}; `content` is then empty.
   */
  thinking?: string;
  /**
   * An incremental tool-call event, sent only when the request set
   * {@link Request.streamEvents}. See {@link ToolCallEvent}.
   */
  toolCall?: ToolCallEvent;
  /**
   * The turn's usage so far, sent only when the request set
   * {@link Request.streamEvents}, each time the provider reports it. A
   * cumulative snapshot, never a delta: counts never go down, and the
   * last one equals the finished turn's usage ({@link StreamOutcome.usage}).
   */
  usage?: Usage;
  done: boolean;
}

/**
 * The step of a streamed tool call: `start` opens it (`index`, `id`,
 * `name`), `delta` carries a fragment of its JSON arguments, `end`
 * closes it (`call` holds it complete).
 */
export type ToolCallEventKind = "start" | "delta" | "end";

/**
 * One step of a tool call as it streams.
 *
 * For each call: one start, then any deltas, then one end, in that
 * order; events of parallel calls may interleave and are told apart by
 * `index`, the call's position in {@link StreamOutcome.toolCalls}. A
 * call that started but never ended (the stream failed, was cancelled
 * or closed) did not complete: no end event is made up for it.
 */
export interface ToolCallEvent {
  kind: ToolCallEventKind;
  index: number;
  /**
   * Set on start and end. `name` is the tool's name as the caller
   * defined it, whatever name went over the wire.
   */
  id?: string;
  name?: string;
  /**
   * A raw fragment of the arguments' JSON, on delta only; fragments
   * concatenate to the complete arguments and are not valid JSON alone.
   */
  argumentsDelta?: string;
  /**
   * The complete call, on end only: exactly the entry at `index` in
   * {@link StreamOutcome.toolCalls}.
   */
  call?: ToolCall;
}

export interface ToolDef {
  name: string;
  description: string;
  parameters: unknown;
  /** Marks a prompt-cache breakpoint after this tool. */
  cacheControl?: CacheControl;
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
  /** The turn's thinking blocks, to replay unchanged. */
  thinking?: ThinkingBlock[];
  finishReason?: string;
  usage?: Usage;
}

/** What a finished stream folded into. */
export interface StreamOutcome {
  content: string;
  /** The turn's thinking blocks in order; replay them unchanged. */
  thinking: ThinkingBlock[];
  toolCalls: ToolCall[];
  finishReason: string;
  usage: Usage;
}

/**
 * A token stream whose finished turn can be read back: `outcome()` is
 * defined only once the final token (`done`) was yielded; after an
 * error it stays undefined.
 */
export interface ToolStream extends AsyncIterable<Token> {
  outcome(): StreamOutcome | undefined;
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

/**
 * Streams a tool-calling completion: text deltas as tokens, then the
 * whole turn (tool calls, stop reason, usage) from
 * {@link ToolStream.outcome}.
 */
export interface ToolStreamer {
  streamWithTools(req: Request, tools: ToolDef[]): ToolStream;
}

export interface Provider
  extends Partial<Completer & Streamer & ToolCaller & ToolStreamer> {
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
  /**
   * Set by the `hermetic` option ({@link LoadOptions}): the adapter
   * reads nothing ambient while it builds and runs, no environment
   * variable (`ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`,
   * `ANTHROPIC_WORKSPACE_ID`, `<SCHEME>_API_KEY`, `ROUTELLM_*`,
   * `LLM_BASE_URL`) and no llm.yaml. Only this config counts.
   */
  hermetic?: boolean;
}

// ─── Errors ──────────────────────────────────────────────────────────────────

export { LLMError };

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

/**
 * The provider refused the credential (HTTP 401). A 403 is the narrower
 * {@link PermissionDeniedError}, still an `AuthError`.
 */
export class AuthError extends LLMError {
  provider: string;

  /** `cause` is the provider's error response, when there is one. */
  constructor(provider: string, cause?: Error) {
    super(
      `authentication failed for provider: ${provider}` +
        (cause ? `: ${cause.message}` : "")
    );
    this.name = "AuthError";
    this.provider = provider;
    if (cause) this.cause = cause;
  }
}

/**
 * The credential was accepted but may not do what was asked (HTTP 403,
 * an Anthropic `permission_error`): a key without access to the model,
 * workspace or feature. Retrying with the same key will not help.
 *
 * It is an auth error too: `err instanceof AuthError` holds, with the
 * same `provider` and `cause`, so code written before the split keeps
 * treating a 403 as an auth failure. Test `PermissionDeniedError` first
 * to tell the two apart.
 */
export class PermissionDeniedError extends AuthError {
  constructor(provider: string, cause?: Error) {
    super(provider, cause);
    this.message =
      `permission denied for provider: ${provider}` +
      (cause ? `: ${cause.message}` : "");
    this.name = "PermissionDeniedError";
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
 * - LLMError with statusCode >= 500 (an {@link HTTPStatusError} from a
 *   5xx response or an overloaded stream)
 * - RateLimitError (429)
 * - Connection/timeout errors (ECONNREFUSED, ETIMEDOUT, ECONNRESET)
 *
 * Never fallbackable: a {@link ToolNameError} (the caller's tool set
 * needs fixing), a {@link StreamTruncatedError} (tokens may already be
 * consumed), a {@link ResponseTooLargeError}, a {@link KeyEndpointError}
 * and an {@link APIError} below 500.
 */
export function isFallbackable(err: Error): boolean {
  if (err instanceof ToolNameError) return false;
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

/** Options for {@link urlSource}. */
export interface URLSourceOptions {
  /** Body cap in bytes (default {@link DEFAULT_MAX_MEDIA_BYTES}). */
  maxBytes?: number;
}

/**
 * URL-backed media; `read()` downloads lazily.
 *
 * The download is guarded against server-side request forgery: http or
 * https only, public addresses only (checked on what is dialed, so DNS
 * rebinding cannot redirect it), no redirects, no proxy, at most
 * `maxBytes` (32 MiB by default; {@link MediaTooLargeError} past it),
 * 30s, and `--offline`. Refusals are {@link MediaURLRefusedError}.
 * Adapters whose provider accepts a URL (Anthropic images and PDFs,
 * OpenAI images) send the URL and never read it. Local or private
 * media: read it yourself and pass {@link inlineSource} or
 * {@link fileSource}.
 */
export function urlSource(url: string, opts: URLSourceOptions = {}): MediaSource {
  const maxBytes = opts.maxBytes && opts.maxBytes > 0 ? opts.maxBytes : DEFAULT_MAX_MEDIA_BYTES;
  return {
    url: () => url,
    mimeType: () => "",
    read: () => fetchMedia(url, maxBytes),
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

// ─── Registry ────────────────────────────────────────────────────────────────

export type Factory = (cfg: ResolvedConfig) => Provider;

/**
 * What an adapter states about its scheme: the endpoints its
 * credentials may go to besides loopback, `LLM_BASE_URL` and the
 * adapter's own base URL variable (see {@link credentialsTrusted}).
 */
export type { Declaration };

const registry = new Map<string, Factory>();

/** Register a provider factory for the given URI scheme. */
export function register(
  scheme: string,
  factory: Factory,
  decl?: Declaration
): void {
  if (registry.has(scheme)) {
    throw new LLMError(`provider already registered for scheme: ${scheme}`);
  }
  registry.set(scheme, factory);
  setDeclaration(scheme, decl);
}

export { declarationOf };

/**
 * Resolve a URI to a Provider instance via the registry. With
 * `opts.hermetic` neither the config nor the adapter reads anything
 * ambient (see {@link LoadOptions}).
 */
export function resolve(uriStr: string, opts: LoadOptions = {}): Provider {
  const cfg = loadConfig(uriStr, opts);
  const factory = registry.get(cfg.uri.scheme);
  if (!factory) {
    throw new ProviderNotFoundError(cfg.uri.scheme);
  }
  return factory(cfg);
}

/** Clear the registry (for tests). */
export function clearRegistry(): void {
  registry.clear();
  clearDeclarations();
}

// ─── Client ──────────────────────────────────────────────────────────────────

export interface ClientOptions extends LoadOptions {
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
  /**
   * Streams a tool-calling completion; falls back like {@link stream},
   * before the first token only.
   */
  streamWithTools(req: Request, tools: ToolDef[]): ToolStream;
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
  const load: LoadOptions = { hermetic: opts?.hermetic };
  const cfg = loadConfig(uri, load);

  // Merge fallbacks: opts > env > none
  const fallbackURIs = opts?.fallback ?? cfg.fallbacks;

  // Resolve primary; cache fallback providers for close()
  const primary = resolve(uri, load);
  const allURIs = [uri, ...fallbackURIs];
  const resolvedProviders = new Map<number, Provider>();
  resolvedProviders.set(0, primary);

  function resolveAt(index: number): Provider {
    let p = resolvedProviders.get(index);
    if (!p) {
      p = resolve(allURIs[index], load);
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

  // Routes a stream through fallback: on creation or first-token
  // failure with a fallbackable/capability error, the next provider is
  // tried. Once a token arrived, errors pass through.
  function fallbackStream(
    request: Request,
    op: "stream" | "streamWithTools",
    open: (p: Provider) => AsyncIterable<Token>
  ): ToolStream {
    let inner: AsyncIterator<Token> | null = null;
    let source: AsyncIterable<Token> | null = null;
    let initialized = false;

    async function first(): Promise<IteratorResult<Token>> {
      opts?.onRequest?.(request);
      const errors: Error[] = [];
      for (let i = 0; i < allURIs.length; i++) {
        try {
          const p = resolveAt(i);
          if (typeof p[op] !== "function") {
            throw new CapabilityNotSupportedError(
              op,
              parseURI(allURIs[i]).scheme
            );
          }
          const iterable = open(p);
          const iter = iterable[Symbol.asyncIterator]();
          // Attempt first read to detect immediate failures
          const head = await iter.next();
          inner = iter;
          source = iterable;
          return head;
        } catch (err) {
          const e = err instanceof Error ? err : new Error(String(err));
          errors.push(e);
          opts?.onError?.(e);
          const canContinue =
            isFallbackable(e) || e instanceof CapabilityNotSupportedError;
          if (!canContinue || i === allURIs.length - 1) {
            if (errors.length > 1) throw new FallbackExhaustedError(errors);
            throw e;
          }
          opts?.onFallback?.(i, i + 1, e);
        }
      }
      throw new FallbackExhaustedError(errors);
    }

    return {
      [Symbol.asyncIterator]() {
        return {
          async next(): Promise<IteratorResult<Token>> {
            if (!initialized) {
              initialized = true;
              return first();
            }
            return inner!.next();
          },
          async return(): Promise<IteratorResult<Token>> {
            await inner?.return?.();
            return { value: undefined, done: true };
          },
        };
      },
      outcome(): StreamOutcome | undefined {
        const s = source as Partial<ToolStream> | null;
        return typeof s?.outcome === "function" ? s.outcome() : undefined;
      },
    };
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
      return fallbackStream(request, "stream", (p) => p.stream!(request));
    },

    streamWithTools(request: Request, tools: ToolDef[]): ToolStream {
      return fallbackStream(request, "streamWithTools", (p) =>
        p.streamWithTools!(request, tools)
      );
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
        {
          content: resp.content,
          role: "assistant",
          usage: resp.usage,
          finishReason: resp.finishReason,
          thinking: resp.thinking,
        },
        Date.now() - start
      );
      return resp;
    },

    capabilities(): string[] {
      const caps: string[] = [];
      if (primary.complete) caps.push("complete");
      if (primary.stream) caps.push("stream");
      if (primary.callWithTools) caps.push("callWithTools");
      if (primary.streamWithTools) caps.push("streamWithTools");
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
