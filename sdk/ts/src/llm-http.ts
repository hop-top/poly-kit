/**
 * The HTTP layer the anthropic and openai adapters share: a JSON POST
 * with bounded retries, no redirects, size-capped bodies and SSE
 * events, and credential scrubbing.
 *
 * @internal
 */

import { LLMError } from "./llm-error";
import { scrubSecrets } from "./llm-endpoint";
import { HTTPStatusError, ResponseTooLargeError } from "./llm-errors";

/** Default cap on a whole non-streamed response body. */
export const DEFAULT_MAX_BODY_BYTES = 32 << 20;
/** Default cap on one SSE event of a stream. */
export const DEFAULT_MAX_EVENT_BYTES = 8 << 20;
/** Longest wait a server may ask for between retries. */
export const MAX_RETRY_AFTER_MS = 60_000;
/** Retries after the first attempt, as the provider SDKs default. */
export const DEFAULT_MAX_RETRIES = 2;

export interface Limits {
  body: number;
  event: number;
}

/**
 * Reads the `max_body_bytes` and `max_event_bytes` provider params over
 * the defaults; anything but a positive byte count throws.
 */
export function limitsFrom(
  provider: string,
  params: Record<string, string> | undefined
): Limits {
  const l: Limits = { body: DEFAULT_MAX_BODY_BYTES, event: DEFAULT_MAX_EVENT_BYTES };
  for (const [name, key] of [
    ["max_body_bytes", "body"],
    ["max_event_bytes", "event"],
  ] as const) {
    const v = params?.[name];
    if (v === undefined) continue;
    const t = v.trim();
    const n = Number(t);
    if (!/^\d+$/.test(t) || !Number.isSafeInteger(n) || n <= 0) {
      throw new LLMError(`${provider}: ${name} must be a positive byte count`);
    }
    l[key] = n;
  }
  return l;
}

/**
 * A non-2xx response, read within the body cap and with every secret
 * scrubbed, for the adapter to classify.
 */
export interface ErrorResponse {
  status: number;
  statusText: string;
  headers: Headers;
  body: string;
}

export interface TransportOptions {
  /** Names the provider in errors (`"anthropic"`, a scheme). */
  provider: string;
  /** Credential values: no error message may contain one. */
  secrets: string[];
  limits: Limits;
  maxRetries?: number;
  /** Maps an error response to the adapter's typed error. */
  mapError: (r: ErrorResponse) => Error;
}

/**
 * POSTs `body` as JSON to `url` and returns the 2xx response. Retries,
 * up to `maxRetries`, connection failures and 408, 409, 429 and 5xx
 * responses (an `x-should-retry` header overrides the status), waiting
 * what `retry-after-ms` / `retry-after` ask within (0, 60s], else an
 * exponential backoff. A redirect is not followed: it fails as an
 * {@link HTTPStatusError}. Error bodies are read within the body cap.
 */
export async function post(
  opts: TransportOptions,
  url: string,
  headers: Record<string, string>,
  body: unknown
): Promise<globalThis.Response> {
  const maxRetries = opts.maxRetries ?? DEFAULT_MAX_RETRIES;
  const payload = JSON.stringify(body);
  for (let attempt = 0; ; attempt++) {
    let resp: globalThis.Response;
    try {
      resp = await globalThis.fetch(url, {
        method: "POST",
        headers,
        body: payload,
        redirect: "manual",
      });
    } catch (err) {
      if (attempt < maxRetries) {
        await sleep(backoff(attempt));
        continue;
      }
      throw scrubError(networkError(opts.provider, err), opts.secrets);
    }
    if (resp.status >= 300 && resp.status < 400) {
      await resp.body?.cancel().catch(() => undefined);
      throw new HTTPStatusError(resp.status, `${opts.provider}: redirect not followed`);
    }
    if (resp.ok) return resp;

    const text = await readBody(resp, opts.limits.body, opts.provider).catch(
      (err: unknown) => {
        throw scrubError(err as Error, opts.secrets);
      }
    );
    if (attempt < maxRetries && shouldRetry(resp)) {
      await sleep(retryDelay(resp.headers, attempt));
      continue;
    }
    throw scrubError(
      opts.mapError({
        status: resp.status,
        statusText: resp.statusText,
        headers: resp.headers,
        body: scrubSecrets(text, opts.secrets),
      }),
      opts.secrets
    );
  }
}

function shouldRetry(resp: globalThis.Response): boolean {
  const h = resp.headers.get("x-should-retry");
  if (h === "true") return true;
  if (h === "false") return false;
  const s = resp.status;
  return s === 408 || s === 409 || s === 429 || s >= 500;
}

/** The wait the server asks for, when within (0, 60s]; else backoff. */
export function retryDelay(headers: Headers, attempt: number): number {
  const ms = retryAfterMs(headers);
  return ms ?? backoff(attempt);
}

/**
 * The `retry-after-ms` or `retry-after` wait in ms, when one is set and
 * within (0, {@link MAX_RETRY_AFTER_MS}]; undefined otherwise (absent,
 * zero, negative, NaN, huge or unparsable).
 */
export function retryAfterMs(headers: Headers): number | undefined {
  const inRange = (ms: number) => ms > 0 && ms <= MAX_RETRY_AFTER_MS;
  const msHeader = headers.get("retry-after-ms");
  if (msHeader !== null && isNumber(msHeader)) {
    const n = Number(msHeader.trim());
    if (inRange(n)) return n;
  }
  const secHeader = headers.get("retry-after");
  if (secHeader !== null) {
    if (isNumber(secHeader)) {
      const n = Number(secHeader.trim()) * 1000;
      if (inRange(n)) return n;
    } else {
      const at = Date.parse(secHeader);
      if (!Number.isNaN(at) && inRange(at - Date.now())) return at - Date.now();
    }
  }
  return undefined;
}

function isNumber(v: string): boolean {
  return /^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/.test(v.trim());
}

/** 0.5s doubling per retry, at most 8s, less up to 25% jitter. */
function backoff(attempt: number): number {
  const delay = Math.min(500 * 2 ** attempt, 8000);
  return delay - Math.floor(Math.random() * (delay / 4));
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

function networkError(provider: string, err: unknown): LLMError {
  const cause = (err as { cause?: { code?: string; message?: string } }).cause;
  const detail = cause?.message ?? (err as Error)?.message ?? String(err);
  const e = new LLMError(`${provider}: request failed: ${detail}`);
  if (cause?.code) e.code = cause.code;
  return e;
}

/**
 * Returns `err` with every secret replaced by `REDACTED` in its message
 * and its cause's: a response body echoing the key never surfaces it.
 */
export function scrubError<E extends Error>(err: E, secrets: readonly string[]): E {
  if (!(err instanceof Error) || secrets.length === 0) return err;
  err.message = scrubSecrets(err.message, secrets);
  if (err.stack) err.stack = scrubSecrets(err.stack, secrets);
  const cause = (err as { cause?: unknown }).cause;
  if (cause instanceof Error && cause !== err) scrubError(cause, secrets);
  return err;
}

/**
 * Reads a whole body as text, failing with a
 * {@link ResponseTooLargeError} once it passes `max` bytes.
 */
export async function readBody(
  resp: globalThis.Response,
  max: number,
  provider: string
): Promise<string> {
  if (!resp.body) return "";
  const reader = resp.body.getReader();
  const chunks: Uint8Array[] = [];
  let read = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      read += value.byteLength;
      if (read > max) {
        await reader.cancel().catch(() => undefined);
        throw new ResponseTooLargeError(provider, "body", max);
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }
  return Buffer.concat(chunks).toString("utf8");
}

/** One server-sent event. */
export interface SSEEvent {
  event: string;
  data: string;
}

/**
 * Yields the events of an SSE body (`\n` or `\r\n` framing, comments
 * skipped, multi-line data joined). An event, counted from the last
 * blank line without carriage returns, longer than `max` bytes fails
 * with a {@link ResponseTooLargeError}. A trailing event without its
 * blank line is not dispatched.
 */
export async function* sseEvents(
  resp: globalThis.Response,
  max: number,
  provider: string
): AsyncGenerator<SSEEvent> {
  if (!resp.body) return;
  const reader = resp.body.getReader();
  const decoder = new TextDecoder();
  let buf: Buffer = Buffer.alloc(0);
  let eventBytes = 0;
  let name = "";
  let data: string[] = [];
  let finished = false;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (value) buf = Buffer.concat([buf, value]);
      let nl: number;
      while ((nl = buf.indexOf(0x0a)) >= 0) {
        let line = buf.subarray(0, nl);
        buf = buf.subarray(nl + 1);
        if (line.length > 0 && line[line.length - 1] === 0x0d) {
          line = line.subarray(0, line.length - 1);
        }
        if (line.length === 0) {
          eventBytes = 0;
          if (data.length > 0 || name !== "") {
            yield { event: name, data: data.join("\n") };
          }
          name = "";
          data = [];
          continue;
        }
        eventBytes += nonCR(line) + 1;
        if (eventBytes > max) throw new ResponseTooLargeError(provider, "event", max);
        const text = decoder.decode(line);
        if (text.startsWith(":")) continue;
        const colon = text.indexOf(":");
        const field = colon < 0 ? text : text.slice(0, colon);
        let v = colon < 0 ? "" : text.slice(colon + 1);
        if (v.startsWith(" ")) v = v.slice(1);
        if (field === "event") name = v;
        else if (field === "data") data.push(v);
      }
      if (eventBytes + nonCR(buf) > max) {
        throw new ResponseTooLargeError(provider, "event", max);
      }
      if (done) {
        finished = true;
        return;
      }
    }
  } finally {
    if (!finished) await reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
}

function nonCR(b: Uint8Array): number {
  let n = 0;
  for (const c of b) if (c !== 0x0d) n++;
  return n;
}

/** Joins a base URL and a path with exactly one slash. */
export function joinURL(base: string, path: string): string {
  return base.replace(/\/+$/, "") + "/" + path.replace(/^\/+/, "");
}
