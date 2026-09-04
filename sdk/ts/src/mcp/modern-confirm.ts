/**
 * @module mcp/modern-confirm
 * @package @hop-top/kit
 *
 * MRTR (multi round-trip requests) confirmation for the modern
 * (2026-07-28) `tools/call` path: the elicitation-based strategy that
 * fills the confirmation-gate slot when a mount is given key material
 * via `confirmationKey`.
 *
 * Flow (spec: basic/patterns/mrtr + client/elicitation): the first
 * call on a `kit/requires-confirmation` leaf returns `resultType`
 * "input_required" carrying a single `elicitation/create` form request
 * under the reserved `inputRequests` key "confirm" plus an
 * integrity-protected `requestState`. The client gathers the user's
 * decision and retries the original call (new JSON-RPC id) with
 * `params.inputResponses` and the echoed `params.requestState`; the
 * gate verifies the state and lets the invocation proceed only when the
 * confirm response's action is "accept". Decline/cancel refuse the call
 * with an isError complete result.
 *
 * Statelessness: everything a retry needs lives inside `requestState`.
 * There is no server-side pending-request storage of any kind, so any
 * instance holding the same key verifies state minted by any other —
 * which is exactly why the key must be adopter-supplied and shared, and
 * why there is deliberately no generated-at-mount default. The
 * corollary (spec: replay warning) is that an accepted state remains
 * honorable for identical (leaf, arguments, principal) until its TTL
 * lapses; single-use redemption would require the server-side state
 * this surface deliberately does not keep, so the short TTL bounds the
 * window instead.
 *
 * Two verification failures are deliberately distinct (ADR 0004): an
 * expired-but-authentic state is a routine re-prompt; a state failing
 * HMAC verification is never honored — the rejection is recorded as a
 * security-relevant audit event first, and only then is a fresh prompt
 * (with newly minted state) issued. Tampering can therefore cost a
 * request, but is never silently treated as a re-prompt.
 *
 * MRTR confirmation is an alternative way to SATISFY confirmation,
 * never a way to bypass the destructive ceiling: the policy gate runs
 * inside `bridge.invoke` after this gate, exactly as on every other
 * path.
 */

import { createHash, createHmac, timingSafeEqual } from 'node:crypto';

import {
  canonicalJSONStringify,
  errorResultBlock,
  META_KEY_CLIENT_CAPABILITIES,
  RESULT_TYPE_INPUT_REQUIRED,
  toolName,
  type Leaf,
} from './types.js';
import { headerValue } from './legacy.js';
import type { McpHttpRequest } from './dispatch.js';

/**
 * The single reserved `inputRequests` key the confirmation flow uses;
 * the retry's answer is read from the same key in
 * `params.inputResponses`.
 */
export const MCP_CONFIRM_KEY = 'confirm';

/**
 * Lifetime of a minted `requestState`, in seconds. Long enough for a
 * human to read and answer the prompt; short enough to bound the replay
 * window the stateless design cannot otherwise close (see the module
 * comment).
 */
export const MCP_CONFIRM_STATE_TTL_SECONDS = 5 * 60;

/**
 * Tags the `requestState` wire format so a future format change
 * invalidates (rather than misparses) old state.
 */
export const MCP_CONFIRM_STATE_VERSION = 'v1';

/**
 * Domain separation for the MAC, so this tag can never be confused with
 * any other HMAC minted under a shared key.
 */
const MAC_DOMAIN = `cmdsurface-mcp-confirm-${MCP_CONFIRM_STATE_VERSION}`;

/**
 * The request context a `requestState` is bound to. The HMAC covers
 * every field plus the expiry, so state presented for a different leaf,
 * different arguments, or by a different principal fails verification
 * outright (spec: reject state presented on a request that does not
 * match / by a different principal).
 */
export interface McpConfirmBinding {
  /** The leaf path key, space-joined, e.g. "widget purge". */
  tool: string;
  /** Hex SHA-256 of the canonically serialized `params.arguments`. */
  argsDigest: string;
  /**
   * Hex SHA-256 of the `Authorization` header value, or "" when the
   * request carried none. Hashed so the MAC input never embeds
   * credential material.
   */
  principal: string;
}

/** The outcome of verifying a presented `requestState`. */
export type McpConfirmStateStatus = 'valid' | 'expired' | 'invalid';

/** One recorded rejection of a state that failed integrity verification. */
export interface McpConfirmRejection {
  path: string;
  mcpConfirmRejection: string;
}

/** A gate decision: the result body to write plus its HTTP status. */
export type McpConfirmDecision = { result: Record<string, unknown>; status: number };

/**
 * The MRTR confirmation gate for mounts configured with a
 * `confirmationKey`.
 *
 * Clients that did not declare form-mode elicitation keep the
 * `X-Confirm-Token` header gate: the spec forbids sending
 * `inputRequests` for a capability the client never declared, and the
 * capability stays optional precisely because this fallback exists — so
 * a missing elicitation capability is never -32021 here.
 */
export class ElicitationConfirmGate {
  private readonly key: Buffer;

  /**
   * Audit records of rejected states, standing in for Go's bridge sink
   * fan-out, which this port does not carry.
   */
  readonly rejections: McpConfirmRejection[] = [];

  constructor(key: Uint8Array) {
    if (key.length === 0) {
      throw new Error('cmdsurface: confirmationKey: empty key');
    }
    this.key = Buffer.from(key);
  }

  /**
   * Runs the gate for one `tools/call`. Returns `undefined` to proceed
   * with the invocation, or the refusal / interim result plus its HTTP
   * status. The caller stamps the modern envelope onto whatever is
   * returned.
   */
  evaluate(
    req: McpHttpRequest,
    leaf: Leaf,
    params: unknown,
  ): McpConfirmDecision | undefined {
    if (!leaf.class?.requiresConfirmation) return undefined;
    if (!clientSupportsFormElicitation(params)) {
      return headerConfirmationGate(req);
    }

    const binding: McpConfirmBinding = {
      tool: leaf.path.join(' '),
      argsDigest: mcpConfirmArgsDigest(params),
      principal: mcpConfirmPrincipal(req),
    };
    const retry = parseMcpConfirmRetry(params);

    if (retry.state === '') {
      // A first call — or a retry that dropped the state it was
      // required to echo, which is indistinguishable from one and
      // equally unverifiable. Prompt (again).
      return { result: this.inputRequired(leaf, binding), status: 200 };
    }

    const status = verifyMcpConfirmState(
      this.key,
      retry.state,
      binding,
      nowSeconds(),
    );
    if (status === 'invalid') {
      // Tampered, malformed, or presented for a different request /
      // principal than it was minted for: never honored, audited, then
      // re-prompted with fresh state (see the module comment).
      this.auditRejection(leaf);
      return { result: this.inputRequired(leaf, binding), status: 200 };
    }
    if (status === 'expired') {
      // Authentic but past its TTL: a routine re-prompt (spec:
      // re-request missing information rather than error), no audit.
      return { result: this.inputRequired(leaf, binding), status: 200 };
    }

    if (retry.action === 'accept') return undefined;
    if (retry.action === 'decline' || retry.action === 'cancel') {
      return { result: errorResultBlock('confirmation declined'), status: 200 };
    }
    // The confirm answer is missing or unusable (absent inputResponses,
    // non-object entry, unknown action): the requested information was
    // not provided, so re-request it rather than erroring (spec SHOULD).
    return { result: this.inputRequired(leaf, binding), status: 200 };
  }

  /**
   * Builds the `input_required` result envelope for one confirmation
   * prompt: the reserved "confirm" `elicitation/create` form request
   * plus freshly minted `requestState` (satisfying the spec MUST of
   * carrying at least one of `inputRequests` / `requestState` — this
   * flow always carries both). The caller stamps the envelope (_meta
   * serverInfo; `resultType` stays "input_required") and writes it at
   * HTTP 200. Interim `input_required` results are never cacheable: no
   * `ttlMs` / `cacheScope` members, ever.
   */
  inputRequired(
    leaf: Leaf,
    binding: McpConfirmBinding,
  ): Record<string, unknown> {
    const expiry = nowSeconds() + MCP_CONFIRM_STATE_TTL_SECONDS;
    return {
      resultType: RESULT_TYPE_INPUT_REQUIRED,
      inputRequests: {
        [MCP_CONFIRM_KEY]: {
          method: 'elicitation/create',
          params: {
            mode: 'form',
            message: `Approve execution of ${JSON.stringify(toolName(leaf.path))}?`,
            // No form fields: the approval rides the elicit action
            // (accept / decline / cancel), so the requested schema is
            // the empty object.
            requestedSchema: { type: 'object', properties: {} },
          },
        },
      },
      requestState: mintMcpConfirmState(this.key, binding, expiry),
    };
  }

  /**
   * Records a failed `requestState` verification as a
   * security-relevant audit event. Go emits this on the bridge's
   * registered sinks; this port has no sink fan-out, so the records
   * accumulate here for adopters and tests to read. Best-effort by the
   * same contract: recording never affects the response.
   */
  private auditRejection(leaf: Leaf): void {
    this.rejections.push({
      path: leaf.path.join(' '),
      mcpConfirmRejection: 'request_state_verification_failed',
    });
  }
}

/**
 * The default gate: a `requiresConfirmation` leaf needs the
 * `X-Confirm-Token` header, exactly as on the legacy path. This is the
 * fallback for clients without form elicitation and the whole gate for
 * mounts without a confirmation key.
 */
export function headerConfirmationGate(
  req: McpHttpRequest,
): McpConfirmDecision | undefined {
  if (headerValue(req, 'x-confirm-token')) return undefined;
  return { result: errorResultBlock('confirmation required'), status: 428 };
}

/**
 * Reports whether the request's
 * `io.modelcontextprotocol/clientCapabilities` declares form-mode
 * elicitation. Per spec, an empty `elicitation` object (`{}`) declares
 * form-only support; a non-empty object must name "form" among its
 * modes (a url-only client cannot receive this flow's form request).
 * Anything that is not a conforming object declaration — key absent,
 * value null or non-object — counts as undeclared, failing toward the
 * header fallback rather than toward sending a request the client never
 * said it could handle.
 */
export function clientSupportsFormElicitation(params: unknown): boolean {
  const meta = plainObject(plainObject(params)?._meta);
  if (meta === undefined) return false;
  const caps = plainObject(meta[META_KEY_CLIENT_CAPABILITIES]);
  if (caps === undefined) return false;
  const modes = plainObject(caps.elicitation);
  if (modes === undefined) return false;
  if (Object.keys(modes).length === 0) return true;
  return 'form' in modes;
}

/**
 * The tolerant read of the MRTR retry members of a `tools/call`
 * request. Members that are absent or of the wrong JSON type simply
 * stay empty — the gate treats a missing state as "prompt" and a
 * missing/unusable action as "re-prompt", so malformed retries converge
 * on a fresh `input_required` rather than a decode error.
 */
export function parseMcpConfirmRetry(params: unknown): {
  state: string;
  action: string;
} {
  const obj = plainObject(params);
  if (obj === undefined) return { state: '', action: '' };

  const state = typeof obj.requestState === 'string' ? obj.requestState : '';

  let action = '';
  const entry = plainObject(plainObject(obj.inputResponses)?.[MCP_CONFIRM_KEY]);
  if (entry !== undefined && typeof entry.action === 'string') {
    action = entry.action;
  }
  return { state, action };
}

/**
 * Derives the principal component of the state binding: hex SHA-256 of
 * the `Authorization` value, "" when the header is absent.
 * Presence-only bearer checking is all this surface does for auth
 * (`class.authRequired`), so the raw header value is the closest stable
 * principal identifier available; hashing keeps credential material out
 * of the MAC input.
 */
export function mcpConfirmPrincipal(req: McpHttpRequest): string {
  const auth = headerValue(req, 'authorization');
  if (!auth) return '';
  return createHash('sha256').update(auth, 'utf8').digest('hex');
}

/**
 * Returns the hex SHA-256 of the canonically serialized
 * `params.arguments`. Canonical form sorts object keys at every nesting
 * level, so equal argument sets digest identically regardless of the
 * client's key order; absent arguments canonicalize to "null". Only the
 * arguments participate: `_meta`, `inputResponses`, and `requestState`
 * all legitimately differ between the first call and its retry, and the
 * tool name is bound separately via the leaf path key.
 */
export function mcpConfirmArgsDigest(params: unknown): string {
  // Go decodes params.arguments into map[string]any, so a non-object
  // value (array, string, number) decodes as absent and canonicalizes
  // to "null" — matched here rather than digesting the raw value.
  const args = plainObject(plainObject(params)?.arguments) ?? null;
  const canonical = canonicalJSONStringify(args);
  return createHash('sha256').update(canonical, 'utf8').digest('hex');
}

/**
 * Computes the HMAC-SHA-256 tag binding a state to its expiry and
 * request context. Each component is written length-prefixed so the
 * concatenation is unambiguous whatever the component contents (no
 * delimiter-injection ambiguity), with a leading domain-separation
 * constant so the tag can never be confused with any other HMAC minted
 * under a shared key.
 */
function mcpConfirmStateMac(
  key: Buffer,
  binding: McpConfirmBinding,
  expiry: number,
): Buffer {
  const mac = createHmac('sha256', key);
  for (const part of [
    MAC_DOMAIN,
    String(expiry),
    binding.tool,
    binding.argsDigest,
    binding.principal,
  ]) {
    // Length-prefixed by BYTE length, matching Go's len(part) over a
    // string's bytes rather than JS's UTF-16 code-unit count.
    mac.update(`${Buffer.byteLength(part, 'utf8')}:${part}`, 'utf8');
  }
  return mac.digest();
}

/**
 * Renders an opaque-to-clients `requestState`:
 * `v1.<expiry-unix>.<base64url(mac)>`. Only the version and expiry
 * travel in the clear; the full binding is reconstructed from the retry
 * request itself at verification time, which is what keeps the state
 * small and the flow stateless.
 */
export function mintMcpConfirmState(
  key: Uint8Array,
  binding: McpConfirmBinding,
  expiry: number,
): string {
  const mac = mcpConfirmStateMac(Buffer.from(key), binding, expiry);
  return `${MCP_CONFIRM_STATE_VERSION}.${expiry}.${mac.toString('base64url')}`;
}

/**
 * Checks a presented state against the current request's binding.
 * Authenticity is decided BEFORE expiry, so "expired" is only ever
 * reported for a state that verifiably came from this key and this
 * exact binding — a tampered expiry fails the MAC and lands in
 * "invalid", never in "expired". Any structural defect (wrong part
 * count, unknown version, non-decimal expiry, undecodable tag) is a
 * verification failure too: a state that cannot be verified is never
 * honored.
 */
export function verifyMcpConfirmState(
  key: Uint8Array,
  state: string,
  binding: McpConfirmBinding,
  now: number,
): McpConfirmStateStatus {
  const parts = state.split('.');
  if (parts.length !== 3 || parts[0] !== MCP_CONFIRM_STATE_VERSION) {
    return 'invalid';
  }
  // Go's strconv.ParseInt is strict decimal: reject anything that is
  // not an optionally-signed run of digits, which JS's Number() would
  // otherwise accept as hex, exponent, or whitespace-padded.
  if (!/^-?\d+$/.test(parts[1])) return 'invalid';
  const expiry = Number(parts[1]);
  if (!Number.isSafeInteger(expiry)) return 'invalid';

  const tag = decodeBase64Url(parts[2]);
  if (tag === undefined) return 'invalid';

  const want = mcpConfirmStateMac(Buffer.from(key), binding, expiry);
  if (tag.length !== want.length || !timingSafeEqual(tag, want)) {
    return 'invalid';
  }
  if (expiry < now) return 'expired';
  return 'valid';
}

/**
 * Strict, canonical base64url decode. Node's `Buffer.from(s,
 * 'base64url')` is lenient — it silently drops non-alphabet bytes —
 * whereas Go's `base64.RawURLEncoding.DecodeString` errors, so the
 * round-trip check rejects anything Go would refuse.
 */
function decodeBase64Url(s: string): Buffer | undefined {
  if (!/^[A-Za-z0-9_-]*$/.test(s)) return undefined;
  const buf = Buffer.from(s, 'base64url');
  if (buf.toString('base64url') !== s) return undefined;
  return buf;
}

/** Whole seconds since the Unix epoch. */
function nowSeconds(): number {
  return Math.floor(Date.now() / 1000);
}

/**
 * Narrows a value to a plain JSON object, the shape every tolerant read
 * above expects. Arrays and null are not objects for this purpose:
 * treating them as absent is what makes malformed input converge on a
 * fresh prompt rather than on a decode error.
 */
function plainObject(v: unknown): Record<string, unknown> | undefined {
  if (v === null || v === undefined || typeof v !== 'object') return undefined;
  if (Array.isArray(v)) return undefined;
  return v as Record<string, unknown>;
}
