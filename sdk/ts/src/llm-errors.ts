/**
 * Typed errors the HTTP adapters return, re-exported from
 * `@hop-top/kit/llm`. Apart from `llm.ts` so the adapters' transport
 * can raise them without an import cycle.
 *
 * @internal
 */

import { LLMError } from "./llm-error";
import { goQuote } from "./llm-quote";

/**
 * An HTTP error response kit classifies by status: a 5xx (fallbackable)
 * or a redirect, which is never followed.
 */
export class HTTPStatusError extends LLMError {
  readonly status: string;

  constructor(statusCode: number, status: string) {
    super(`http ${statusCode}: ${status}`);
    this.name = "HTTPStatusError";
    this.statusCode = statusCode;
    this.status = status;
  }
}

/**
 * A provider error response no other class covers (a 4xx such as an
 * invalid request). `type` is the provider's error type, when it gave
 * one. Not fallbackable.
 */
export class APIError extends LLMError {
  readonly provider: string;
  readonly type: string;

  constructor(provider: string, statusCode: number, type: string, message: string) {
    super(message);
    this.name = "APIError";
    this.provider = provider;
    this.statusCode = statusCode;
    this.type = type;
  }
}

/**
 * A stream ended before the provider's terminal event: the response is
 * incomplete, never a success. Not fallbackable: tokens may already
 * have been consumed.
 */
export class StreamTruncatedError extends LLMError {
  readonly provider: string;

  constructor(provider: string) {
    super(
      `stream truncated (provider ${JSON.stringify(provider)}): ended before its final event`
    );
    this.name = "StreamTruncatedError";
    this.provider = provider;
  }
}

/**
 * A response body, or one streamed event, exceeded its size limit
 * (`max_body_bytes`, `max_event_bytes`). Not fallbackable.
 */
export class ResponseTooLargeError extends LLMError {
  readonly provider: string;
  /** The part that overran: `"body"` or `"event"`. */
  readonly what: "body" | "event";
  readonly limit: number;

  constructor(provider: string, what: "body" | "event", limit: number) {
    super(
      `response ${what} exceeds ${limit} bytes (provider ${JSON.stringify(provider)})`
    );
    this.name = "ResponseTooLargeError";
    this.provider = provider;
    this.what = what;
    this.limit = limit;
  }
}

/**
 * A provider key kit will not send to an endpoint. `endpoint` is masked
 * by `redactURI`; the key never appears.
 */
export class KeyEndpointError extends LLMError {
  readonly scheme: string;
  readonly endpoint: string;
  /** Set when the refusal is plain http to a remote host. */
  readonly insecure: boolean;
  /**
   * Set when the endpoint carries userinfo, a backslash, a
   * percent-escape, whitespace or a control character, or does not
   * parse: no credential goes to it.
   */
  readonly malformed: boolean;

  constructor(
    scheme: string,
    endpoint: string,
    opts: { insecure?: boolean; malformed?: boolean } = {}
  ) {
    super(keyEndpointMessage(scheme, endpoint, opts));
    this.name = "KeyEndpointError";
    this.scheme = scheme;
    this.endpoint = endpoint;
    this.insecure = opts.insecure ?? false;
    this.malformed = opts.malformed ?? false;
  }
}

function keyEndpointMessage(
  scheme: string,
  endpoint: string,
  opts: { insecure?: boolean; malformed?: boolean }
): string {
  const q = goQuote(endpoint);
  if (opts.malformed) {
    return (
      `llm: refusing to send the ${scheme} API key to ${q}: an endpoint must parse and carry ` +
      "no userinfo, backslash, percent-escape, whitespace or control character"
    );
  }
  if (opts.insecure) {
    return `llm: refusing to send the ${scheme} API key over plain http to ${q}; use https or a loopback host`;
  }
  return (
    `llm: refusing to send the ${scheme} API key to ${q}, which only the URI names; ` +
    "set it as llm.yaml base_url or LLM_BASE_URL, or pass api_key in the URI"
  );
}

/**
 * No key was found for a scheme whose adapter requires one. Carries
 * names only, never a value: `envVars` lists every variable consulted,
 * highest precedence first, ending with `LLM_API_KEY`.
 */
export class MissingKeyError extends LLMError {
  readonly scheme: string;
  readonly model: string;
  readonly envVars: string[];

  constructor(scheme: string, model: string, envVars: string[]) {
    const m = model !== "" ? ` (model ${goQuote(model)})` : "";
    super(`llm: no API key for provider ${goQuote(scheme)}${m}: set ${envVars.join(" or ")}`);
    this.name = "MissingKeyError";
    this.scheme = scheme;
    this.model = model;
    this.envVars = envVars;
  }
}
