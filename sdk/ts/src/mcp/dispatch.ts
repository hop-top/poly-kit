/**
 * @module mcp/dispatch
 * @package @hop-top/kit
 *
 * Era detection + dispatch for the dual-spec MCP surface, plus the
 * framework-free handler export.
 *
 * Hosting model (ADR 0043 §2): this module exports a
 * transport-agnostic request handler, not a server. Binding it to
 * node:http, hono, express, fastify, or a Worker is the adopter's
 * job — kit does not own the adopter's HTTP stack, and a pure
 * request→response function is directly testable against the wire
 * fixtures with no socket.
 *
 * Detection implements ADR 0042's normative rules literally: the
 * marker set M1-M4, the two deliberate non-markers, and the
 * precedence chain D1-D4. It deliberately does NOT delegate to the
 * v2 SDK's own `classifyInboundRequest`: that classifier treats a
 * modern `MCP-Protocol-Version` header as a routing signal and
 * routes `initialize`-plus-modern-envelope to the modern era, both
 * of which contradict ADR 0042 (and the wire fixtures, which are the
 * parity contract). The SDK is still the source of the protocol
 * constants and error codes — see types.ts.
 */

import {
  MCP_ERR_INTERNAL,
  META_KEY_PROTOCOL_VERSION,
  HEADER_MCP_METHOD,
  HEADER_MCP_NAME,
  RawJSON,
  resolveMcpConfig,
  type McpBridge,
  type McpMountOptions,
  type ResolvedMcpConfig,
} from './types.js';
import {
  headerValues,
  LegacyMcpHandler,
  parseErrorResponse,
  writeError,
} from './legacy.js';
import { ModernMcpHandler } from './modern.js';

// --- normalized request / response --------------------------------------

/**
 * A normalized inbound request: method, headers, and body bytes. A
 * header may repeat, which the modern header checks treat as
 * significant, so values may be an array.
 */
export interface McpHttpRequest {
  /** HTTP method, e.g. "POST". */
  method: string;
  /** Header names are matched case-insensitively. */
  headers?: Record<string, string | string[] | undefined>;
  /** Raw body bytes, exactly as received — never re-encoded. */
  body?: string;
}

/** A normalized response: status, headers, and body bytes. */
export interface McpHttpResponse {
  status: number;
  headers: Record<string, string>;
  body: string;
}

/** Builds a JSON response with the surface's standard content type. */
export function jsonResponse(status: number, body: string): McpHttpResponse {
  return {
    status,
    headers: { 'Content-Type': 'application/json' },
    body,
  };
}

/** The 202 a modern notification receives: empty body, not processed. */
export function acceptedResponse(): McpHttpResponse {
  return { status: 202, headers: {}, body: '' };
}

// --- parsed request -----------------------------------------------------

/**
 * A decoded JSON-RPC request envelope. `id` is kept as a raw JSON
 * fragment so it round-trips byte-for-byte (Go uses json.RawMessage
 * for exactly this reason), and its absence is distinguishable from
 * an explicit null.
 */
export interface JsonRpcRequest {
  jsonrpc?: string;
  id?: RawJSON;
  method: string;
  params?: unknown;
}

/** Which handler a request routes to. */
export type McpEra = 'legacy' | 'modern';

/**
 * Implements ADR 0042's per-request era detection, precedence D1-D4.
 * Never fails — it only classifies an already-parsed request; D1
 * (parse) is the caller's responsibility.
 *
 * Markers:
 *   M1 — Mcp-Method header present
 *   M2 — Mcp-Name header present
 *   M3 — params._meta contains the reserved protocolVersion key
 *        (key presence only; the value is not inspected here)
 *   M4 — method == "server/discover"
 *
 * Deliberate non-markers, both of which are the subtle part and the
 * most likely source of silent divergence:
 *   - Bare params._meta. 2024-11-05 clients legitimately send
 *     _meta.progressToken and OTel traceparent; only the reserved
 *     protocolVersion key signals modern.
 *   - The MCP-Protocol-Version header, at any value. It predates
 *     2026-07-28, so clients that negotiated down to legacy send it
 *     on every subsequent request; treating it as a modern signal
 *     would serve their handshake and then brick the session.
 */
export function detectMcpEra(
  req: McpHttpRequest,
  rpc: JsonRpcRequest,
): McpEra {
  // D2 — initialize is legacy, unconditionally, even when modern
  // markers are present. The spec's dual-era rule says initialize
  // selects legacy semantics; a confused client gets a working legacy
  // handshake, the most recoverable outcome.
  if (rpc.method === 'initialize') return 'legacy';

  // M4 — method == "server/discover".
  if (rpc.method === 'server/discover') return 'modern';

  // M1 / M2 — header presence.
  if (headerValues(req, HEADER_MCP_METHOD).length > 0) return 'modern';
  if (headerValues(req, HEADER_MCP_NAME).length > 0) return 'modern';

  // M3 — params._meta carries the reserved protocolVersion key.
  if (hasModernMetaMarker(rpc.params)) return 'modern';

  // D4 — no markers: legacy. This is the byte-for-byte preservation
  // path: every request a legacy client can send takes today's exact
  // code path.
  return 'legacy';
}

/**
 * M3: whether params._meta carries the reserved protocolVersion key.
 * Only key presence is tested; the value is never inspected at
 * detection time. Malformed or non-object params/_meta are treated as
 * "no marker" — detection never fails, and shape errors are the
 * handlers' business.
 */
export function hasModernMetaMarker(params: unknown): boolean {
  if (params === null || typeof params !== 'object' || Array.isArray(params)) {
    return false;
  }
  const meta = (params as Record<string, unknown>)._meta;
  if (meta === null || typeof meta !== 'object' || Array.isArray(meta)) {
    return false;
  }
  return META_KEY_PROTOCOL_VERSION in (meta as Record<string, unknown>);
}

// --- handler ------------------------------------------------------------

/** A framework-free MCP request handler. */
export type McpHandler = (
  req: McpHttpRequest,
) => Promise<McpHttpResponse>;

/**
 * Builds the MCP handler for a bridge. Mirrors Go's
 * `MountMCP(bridge, router, opts...)`, minus the router: the caller
 * binds the returned function to whatever HTTP stack it uses.
 *
 * Defaults match Go exactly: both eras enabled, path "/mcp", empty
 * origin allowlist, cache hints ttlMs=0 / cacheScope="private".
 * The two mount-time errors are reproduced rather than absorbed —
 * an explicit empty spec-version set, and a negative ttl or
 * unrecognized cache scope, both throw here.
 */
export function createMcpHandler(
  bridge: McpBridge,
  opts: McpMountOptions = {},
): McpHandler {
  const cfg = resolveMcpConfig(opts);
  const dispatcher = new McpDispatcher(bridge, cfg);
  return (req) => dispatcher.handle(req);
}

/** The resolved mount config for a handler, for adopters that need it. */
export function mcpMountConfig(
  opts: McpMountOptions = {},
): ResolvedMcpConfig {
  return resolveMcpConfig(opts);
}

/**
 * Routes each request to exactly one era's handler. Holds no mutable
 * per-request state; both handlers are safe to share concurrently.
 */
export class McpDispatcher {
  private readonly legacy: LegacyMcpHandler;
  private readonly modern: ModernMcpHandler;

  constructor(
    bridge: McpBridge,
    private readonly cfg: ResolvedMcpConfig,
  ) {
    this.legacy = new LegacyMcpHandler(bridge, cfg);
    this.modern = new ModernMcpHandler(bridge, cfg);
  }

  async handle(req: McpHttpRequest): Promise<McpHttpResponse> {
    const verb = (req.method || 'POST').toUpperCase();
    if (verb !== 'POST') {
      // GET and DELETE answer 405 when the modern era is enabled
      // (spec SHOULD for post-session servers). With only the legacy
      // era mounted there is no such route at all, matching Go, where
      // those verbs are simply never registered.
      if (this.cfg.modernEnabled && (verb === 'GET' || verb === 'DELETE')) {
        return methodNotAllowedResponse();
      }
      return methodNotAllowedResponse();
    }

    // D1 — read and parse the body once. An unreadable body is
    // -32603 @ 400; unparseable JSON is -32700 @ 400 — both
    // byte-identical to the legacy responses, regardless of any
    // headers present.
    const body = req.body;
    if (body === undefined) {
      return writeError(
        undefined,
        {
          code: MCP_ERR_INTERNAL,
          message: 'read request body: unexpected EOF',
        },
        400,
      );
    }

    let rpc: JsonRpcRequest;
    try {
      rpc = parseJsonRpcRequest(body);
    } catch {
      return parseErrorResponse(body);
    }

    // Modern-only mounts route everything to the modern handler and
    // handle it per the normal V1-V9 order — no special-casing of
    // initialize anywhere, since D2 exists to hand a confused client
    // to a legacy handler that is not mounted here.
    if (this.cfg.modernEnabled && !this.cfg.legacyEnabled) {
      return this.modern.serve(req, rpc);
    }
    // Legacy-only mounts ignore markers exactly as the pre-dual-spec
    // surface did.
    if (this.cfg.legacyEnabled && !this.cfg.modernEnabled) {
      return this.legacy.serve(req, rpc);
    }

    switch (detectMcpEra(req, rpc)) {
      case 'modern':
        return this.modern.serve(req, rpc);
      default:
        return this.legacy.serve(req, rpc);
    }
  }
}

/** The 405 GET/DELETE answer at the mount path. */
export function methodNotAllowedResponse(): McpHttpResponse {
  return jsonResponse(
    405,
    '{"jsonrpc":"2.0","error":{"code":-32600,"message":"method not allowed"}}\n',
  );
}

/**
 * Parses one JSON-RPC request, preserving the `id` as raw bytes.
 * Throws on unparseable JSON so the caller can render D1's -32700.
 */
export function parseJsonRpcRequest(body: string): JsonRpcRequest {
  const decoded: unknown = JSON.parse(body);
  if (decoded === null || typeof decoded !== 'object' || Array.isArray(decoded)) {
    // Go decodes a non-object body into its request struct and fails,
    // which surfaces as a parse error at the same place.
    throw new SyntaxError('not a JSON-RPC object');
  }
  const obj = decoded as Record<string, unknown>;
  const out: JsonRpcRequest = {
    method: typeof obj.method === 'string' ? obj.method : '',
  };
  if (typeof obj.jsonrpc === 'string') out.jsonrpc = obj.jsonrpc;
  if ('id' in obj) out.id = new RawJSON(rawIdBytes(body, obj.id));
  if ('params' in obj) out.params = obj.params;
  return out;
}

/**
 * Re-serializes the request id. The id shapes JSON-RPC permits
 * (string, number, null) all round-trip identically through
 * JSON.stringify, which is what Go's json.RawMessage echo produces
 * for the same inputs. `_body` is accepted so a future
 * exactness-sensitive change (e.g. preserving a number's original
 * literal spelling) has the source text to work from.
 */
function rawIdBytes(_body: string, id: unknown): string {
  return JSON.stringify(id) ?? 'null';
}
