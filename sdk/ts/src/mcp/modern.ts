/**
 * @module mcp/modern
 * @package @hop-top/kit
 *
 * The 2026-07-28 MCP handler: the stateless request core behind the
 * era dispatcher. Implements ADR 0042's validation order V1-V9,
 * `server/discover`, the modern error writers (-32020 / -32021 /
 * -32022 with their status mapping), result-envelope stamping
 * (`resultType` + serverInfo `_meta`), and cache-hint application.
 *
 * Statelessness is the revision's core contract: every request
 * carries protocol version, client identity, and capabilities in
 * `params._meta` under reserved io.modelcontextprotocol/* keys;
 * there is no initialize/initialized handshake and no session id.
 * The handler holds only immutable mount configuration, so any
 * instance can serve any request.
 */

import {
  buildToolEnvelope,
  errorResultBlock,
  HEADER_MCP_METHOD,
  HEADER_MCP_NAME,
  HEADER_MCP_PROTOCOL_VERSION,
  MCP_ERR_HEADER_MISMATCH,
  MCP_ERR_INVALID_PARAMS,
  MCP_ERR_INVALID_REQUEST,
  MCP_ERR_METHOD_NOT_FOUND,
  MCP_ERR_UNSUPPORTED_VERSION,
  MCP_SPEC_2026_07_28,
  META_KEY_CLIENT_CAPABILITIES,
  META_KEY_CLIENT_INFO,
  META_KEY_PROTOCOL_VERSION,
  META_KEY_SERVER_INFO,
  RESULT_TYPE_COMPLETE,
  renderCallResult,
  SURFACE_MCP,
  type Leaf,
  type McpBridge,
  type RawJSON,
  type ResolvedMcpConfig,
} from './types.js';
import {
  DestructiveBlockedError,
  SurfaceNotEnabledError,
  UnknownCommandError,
} from './types.js';
import {
  errorMessage,
  headerValue,
  headerValues,
  isExposed,
  resolveLeaf,
  writeError,
  writeResult,
} from './legacy.js';
import { isTaskMethod, taskMethodNotFound } from './tasks.js';
import {
  ElicitationConfirmGate,
  headerConfirmationGate,
  type McpConfirmDecision,
} from './modern-confirm.js';
import type {
  JsonRpcRequest,
  McpHttpRequest,
  McpHttpResponse,
} from './dispatch.js';
import { acceptedResponse } from './dispatch.js';

/** One validation-chain failure: code, message, status, optional data. */
interface CheckError {
  code: number;
  msg: string;
  status: number;
  data?: unknown;
}

/** The decoded view of the reserved params._meta keys (V3). */
export interface ModernRequestMeta {
  version: string;
  versionIsText: boolean;
  versionRaw: unknown;
  clientName?: string;
  clientVersion?: string;
  hasClientInfo: boolean;
}

/** Base64 sentinel delimiters for header values (spec: Value Encoding). */
const SENTINEL_PREFIX = '=?base64?';
const SENTINEL_SUFFIX = '?=';

/** The 2026-07-28 handler. */
export class ModernMcpHandler {
  /**
   * The MRTR confirmation strategy, installed only when the mount was
   * given key material. Without a key there is nothing to MAC state
   * with, so every client — elicitation-capable or not — keeps the
   * `X-Confirm-Token` header gate.
   */
  readonly confirmGate?: ElicitationConfirmGate;

  constructor(
    private readonly bridge: McpBridge,
    private readonly cfg: ResolvedMcpConfig,
  ) {
    if (cfg.confirmationKey !== undefined) {
      this.confirmGate = new ElicitationConfirmGate(cfg.confirmationKey);
    }
  }

  /**
   * The modern entry point. The validation chain runs in ADR 0042's
   * order V1-V9; the first failure responds and stops. HTTP status is
   * 400/404 only where the spec mandates it — application-level
   * JSON-RPC errors ride HTTP 200, matching legacy's convention.
   */
  async serve(
    req: McpHttpRequest,
    rpc: JsonRpcRequest,
  ): Promise<McpHttpResponse> {
    // Origin allowlist (opt-in): modern path only, before any
    // protocol validation.
    if (!this.originAllowed(req)) {
      return this.writeModernError(rpc, {
        code: MCP_ERR_INVALID_REQUEST,
        msg: 'origin not allowed',
        status: 403,
      });
    }

    // V1 — jsonrpc member absent or "2.0" (same tolerance as legacy).
    if (rpc.jsonrpc !== undefined && rpc.jsonrpc !== '2.0') {
      return this.writeModernError(rpc, {
        code: MCP_ERR_INVALID_REQUEST,
        msg: 'invalid jsonrpc version',
        status: 400,
      });
    }

    // V2 — id absent → notification (HTTP 202, empty body, discarded);
    // present id MUST be a string or an integer. null, boolean, float,
    // object, and array ids are all malformed.
    if (rpc.id === undefined) {
      return acceptedResponse();
    }
    if (!validModernRequestId(rpc.id)) {
      return this.writeModernError(rpc, {
        code: MCP_ERR_INVALID_REQUEST,
        msg: `invalid request id: must be a string or integer, got ${rpc.id.raw}`,
        status: 400,
      });
    }

    // V3 — params._meta carries the required reserved keys.
    const parsed = parseModernMeta(rpc.params);
    if ('error' in parsed) {
      return this.writeModernError(rpc, parsed.error);
    }
    const meta = parsed.meta;

    // V4 — MCP-Protocol-Version header present and equal to the _meta
    // protocolVersion value. Conflicting duplicates are themselves a
    // mismatch, decided without comparing either value to the body.
    const proto = singleHeaderValue(req, HEADER_MCP_PROTOCOL_VERSION);
    if (!proto.ok) {
      return this.writeModernError(rpc, {
        code: MCP_ERR_HEADER_MISMATCH,
        msg: `${HEADER_MCP_PROTOCOL_VERSION} header sent with conflicting duplicate values`,
        status: 400,
      });
    }
    if (proto.value === '') {
      return this.writeModernError(rpc, {
        code: MCP_ERR_HEADER_MISMATCH,
        msg: `missing ${HEADER_MCP_PROTOCOL_VERSION} header`,
        status: 400,
      });
    }
    // A non-string _meta value can never equal the header string.
    if (!meta.versionIsText || proto.value !== meta.version) {
      return this.writeModernError(rpc, {
        code: MCP_ERR_HEADER_MISMATCH,
        msg:
          `${HEADER_MCP_PROTOCOL_VERSION} header ${JSON.stringify(proto.value)} ` +
          `does not match _meta protocolVersion ${goFormatValue(meta.versionRaw)}`,
        status: 400,
      });
    }

    // V5 — requested version supported. This handler supports exactly
    // "2026-07-28"; the supported list deliberately excludes
    // "2024-11-05", which is reachable only through its handshake.
    if (meta.version !== MCP_SPEC_2026_07_28) {
      return this.writeModernError(rpc, {
        code: MCP_ERR_UNSUPPORTED_VERSION,
        msg: `unsupported protocol version: ${meta.version}`,
        status: 400,
        data: {
          supported: [MCP_SPEC_2026_07_28],
          requested: meta.versionRaw,
        },
      });
    }

    // V6 — Mcp-Method header presence and header/body agreement.
    const methodErr = this.validateMethodHeader(req, rpc);
    if (methodErr) return this.writeModernError(rpc, methodErr);

    // V8 — method routing. V7 (Mcp-Name) and V9 (per-method params)
    // run inside the method handlers.
    switch (rpc.method) {
      case 'server/discover':
        return this.handleDiscover(rpc);
      case 'tools/list':
        return this.handleToolsList(rpc);
      case 'tools/call':
        return this.handleToolsCall(req, rpc, meta);
      default:
        // The tasks extension is not supported: capabilities.extensions
        // is omitted from server/discover, so tasks/* is simply an
        // unknown method (-32601 @ 404), like any other.
        if (isTaskMethod(rpc.method)) {
          return this.writeModernError(rpc, taskMethodNotFound(rpc.method));
        }
        return this.writeModernError(rpc, {
          code: MCP_ERR_METHOD_NOT_FOUND,
          msg: `method not found: ${rpc.method}`,
          status: 404,
        });
    }
  }

  /**
   * server/discover — the mandatory modern discovery method. Carries
   * no listChanged flag (notifications unimplemented), no extensions
   * map (none supported, which is what makes tasks/* method-not-found),
   * and no instructions.
   */
  private handleDiscover(rpc: JsonRpcRequest): McpHttpResponse {
    const res: Record<string, unknown> = {
      supportedVersions: [MCP_SPEC_2026_07_28],
      capabilities: { tools: {} },
    };
    this.applyCacheHints(res);
    this.stampResultEnvelope(res);
    return writeResult(rpc.id, res, 200);
  }

  /**
   * Modern tools/list: the same tool envelopes the legacy handler
   * emits (so schema drift between eras cannot happen), wrapped in
   * the modern complete-result envelope with cache hints. Pagination
   * is not implemented — a cursor param is ignored, no nextCursor.
   */
  private handleToolsList(rpc: JsonRpcRequest): McpHttpResponse {
    const tools: Array<Record<string, unknown>> = [];
    for (const leaf of this.bridge.leaves()) {
      if (!isExposed(leaf)) continue;
      tools.push(buildToolEnvelope(leaf));
    }
    const res: Record<string, unknown> = { tools };
    this.applyCacheHints(res);
    this.stampResultEnvelope(res);
    return writeResult(rpc.id, res, 200);
  }

  /**
   * Modern tools/call: V7, V9, the legacy pre-flight gates, invoke,
   * render. The shared bridge invoke path keeps the policy gate
   * identical across eras — there is no way to reach a leaf here that
   * the legacy path would have blocked. Results carry no cache hints.
   */
  private async handleToolsCall(
    req: McpHttpRequest,
    rpc: JsonRpcRequest,
    meta: ModernRequestMeta,
  ): Promise<McpHttpResponse> {
    // V7 — Mcp-Name agreement, run against a pre-decode peek of
    // params.name so a header failure is reported even when the rest
    // of params is unparseable.
    const peek = rawToolCallName(rpc.params);
    const nameErr = this.validateNameHeader(req, peek);
    if (nameErr) return this.writeModernError(rpc, nameErr);

    const params = (rpc.params ?? {}) as {
      name?: unknown;
      arguments?: Record<string, unknown>;
    };
    const name = typeof params.name === 'string' ? params.name : '';

    // V9 — per-method params. Unreachable through a conforming
    // request now that V7 requires a present, non-empty, matching
    // name; kept as a defensive internal check for any future caller
    // that bypasses the V7 gate.
    if (name === '') {
      return writeError(
        rpc.id,
        { code: MCP_ERR_INVALID_PARAMS, message: 'missing tool name' },
        200,
      );
    }

    const leaf = resolveLeaf(this.bridge, name);
    if (leaf === undefined || !isExposed(leaf)) {
      return writeError(
        rpc.id,
        { code: MCP_ERR_INVALID_PARAMS, message: `unknown tool: ${name}` },
        200,
      );
    }

    // Pre-flight gates, mirroring legacy exactly.
    if (leaf.class?.authRequired && !headerValue(req, 'authorization')) {
      return this.writeCallError(rpc, 'authentication required', 401);
    }
    // Confirmation gate. With a mounted key this is the MRTR
    // elicitation loop, which falls back to the header gate for
    // clients that did not declare form elicitation; without a key it
    // IS the header gate, for everyone. Either way the decision is a
    // pre-flight refusal or an interim prompt, stamped with the modern
    // envelope like every other result on this path.
    const gated = this.confirmationGate(req, leaf, rpc.params);
    if (gated !== undefined) {
      return writeResult(
        rpc.id,
        this.stampResultEnvelope(gated.result),
        gated.status,
      );
    }

    try {
      const res = await this.bridge.invoke({
        path: [...leaf.path],
        flags: params.arguments,
        meta: {
          surface: SURFACE_MCP,
          requestedAt: new Date(),
          extra: modernInvocationExtra(meta),
        },
      });
      const out = renderCallResult(res);
      if (res.data !== undefined && res.data !== null) {
        out.structuredContent = res.data;
      }
      return writeResult(rpc.id, this.stampResultEnvelope(out), 200);
    } catch (err) {
      if (
        err instanceof UnknownCommandError ||
        err instanceof SurfaceNotEnabledError
      ) {
        return writeError(
          rpc.id,
          { code: MCP_ERR_INVALID_PARAMS, message: `unknown tool: ${name}` },
          200,
        );
      }
      // DestructiveBlockedError and every other invoke failure are
      // complete isError results at HTTP 200, as on legacy.
      return this.writeCallError(rpc, errorMessage(err), 200);
    }
  }

  /**
   * Selects and runs the confirmation strategy for one `tools/call`.
   * Returns `undefined` to proceed with the invocation.
   *
   * A leaf that does not require confirmation is never gated. A mount
   * without a `confirmationKey` has no state to MAC, so the header gate
   * applies to every client — this is the byte-for-byte preservation
   * path, identical to what the legacy handler does.
   */
  private confirmationGate(
    req: McpHttpRequest,
    leaf: Leaf,
    params: unknown,
  ): McpConfirmDecision | undefined {
    if (!leaf.class?.requiresConfirmation) return undefined;
    if (this.confirmGate === undefined) return headerConfirmationGate(req);
    return this.confirmGate.evaluate(req, leaf, params);
  }

  /** V6: Mcp-Method header presence + agreement with the body method. */
  private validateMethodHeader(
    req: McpHttpRequest,
    rpc: JsonRpcRequest,
  ): CheckError | undefined {
    const hdr = singleHeaderValue(req, HEADER_MCP_METHOD);
    if (!hdr.ok) {
      return {
        code: MCP_ERR_HEADER_MISMATCH,
        msg: `${HEADER_MCP_METHOD} header sent with conflicting duplicate values`,
        status: 400,
      };
    }
    if (hdr.value === '') {
      return {
        code: MCP_ERR_HEADER_MISMATCH,
        msg: `missing ${HEADER_MCP_METHOD} header`,
        status: 400,
      };
    }
    if (hdr.value !== rpc.method) {
      return {
        code: MCP_ERR_HEADER_MISMATCH,
        msg:
          `${HEADER_MCP_METHOD} header ${JSON.stringify(hdr.value)} ` +
          `does not match body method ${JSON.stringify(rpc.method)}`,
        status: 400,
      };
    }
    return undefined;
  }

  /**
   * V7: on tools/call the Mcp-Name header MUST be present, non-empty
   * after Base64-sentinel decoding, and byte-equal to params.name,
   * which MUST itself be present. Any violation is a header failure
   * (-32020 @ 400), decided ahead of any body-shape check: headers
   * are the routing signal gateways trust without parsing the body,
   * so an empty or contradicted signal is malformed at the header
   * layer.
   */
  private validateNameHeader(
    req: McpHttpRequest,
    peek: { name: string; present: boolean; isString: boolean },
  ): CheckError | undefined {
    const hdr = singleHeaderValue(req, HEADER_MCP_NAME);
    if (!hdr.ok) {
      return {
        code: MCP_ERR_HEADER_MISMATCH,
        msg: `${HEADER_MCP_NAME} header sent with conflicting duplicate values`,
        status: 400,
      };
    }
    if (hdr.value === '') {
      return {
        code: MCP_ERR_HEADER_MISMATCH,
        msg: `missing ${HEADER_MCP_NAME} header`,
        status: 400,
      };
    }
    const decoded = decodeMcpSentinel(hdr.value);
    if (decoded === undefined) {
      return {
        code: MCP_ERR_HEADER_MISMATCH,
        msg: `${HEADER_MCP_NAME} header value is not valid base64-sentinel encoded`,
        status: 400,
      };
    }
    if (decoded === '') {
      return {
        code: MCP_ERR_HEADER_MISMATCH,
        msg: `${HEADER_MCP_NAME} header decodes to an empty value`,
        status: 400,
      };
    }
    if (!peek.present) {
      return {
        code: MCP_ERR_HEADER_MISMATCH,
        msg: `${HEADER_MCP_NAME} header present but body params.name is absent`,
        status: 400,
      };
    }
    if (!peek.isString) {
      // params.name exists but is not a JSON string: V7 cannot
      // evaluate agreement, so it defers to V9's params decode.
      return undefined;
    }
    if (decoded !== peek.name) {
      return {
        code: MCP_ERR_HEADER_MISMATCH,
        msg:
          `${HEADER_MCP_NAME} header ${JSON.stringify(decoded)} ` +
          `does not match body params.name ${JSON.stringify(peek.name)}`,
        status: 400,
      };
    }
    return undefined;
  }

  /**
   * Adds the members every modern result envelope carries: resultType
   * and a result-level _meta with serverInfo built from the mount's
   * configured identity. A producer that already chose a resultType
   * keeps it (the MRTR confirmation gate stamps "input_required").
   */
  stampResultEnvelope(m: Record<string, unknown>): Record<string, unknown> {
    if (!('resultType' in m)) {
      m.resultType = RESULT_TYPE_COMPLETE;
    }
    m._meta = {
      [META_KEY_SERVER_INFO]: {
        name: this.cfg.serverName,
        version: this.cfg.serverVersion,
      },
    };
    return m;
  }

  /**
   * Adds ttlMs and cacheScope to a cacheable complete-result —
   * server/discover and tools/list only; tools/call carries none.
   * Defaults are ttlMs 0 (immediately stale: the leaf set can mutate
   * at runtime and no list_changed notification exists) and
   * cacheScope "private".
   */
  applyCacheHints(m: Record<string, unknown>): Record<string, unknown> {
    m.ttlMs = this.cfg.cacheTtlMs;
    m.cacheScope = this.cfg.cacheScope;
    return m;
  }

  /** Opt-in Origin allowlist; a request without Origin is never refused. */
  private originAllowed(req: McpHttpRequest): boolean {
    if (this.cfg.originAllowlist.length === 0) return true;
    const origin = headerValue(req, 'origin');
    if (!origin) return true;
    return this.cfg.originAllowlist.includes(origin);
  }

  /**
   * Writes a modern JSON-RPC error. When the rejected request's
   * method is "initialize" the message additionally names the
   * supported versions: a legacy client has no fall-forward
   * mechanism, so the version list in the error text is its only
   * recovery hint.
   */
  private writeModernError(
    rpc: JsonRpcRequest,
    e: CheckError,
  ): McpHttpResponse {
    let msg = e.msg;
    if (rpc.method === 'initialize') {
      msg += `; supported protocol versions: ${MCP_SPEC_2026_07_28}`;
    }
    return writeError(
      rpc.id,
      { code: e.code, message: msg, data: e.data },
      e.status,
    );
  }

  /** An isError tools/call result with the modern envelope stamped on. */
  private writeCallError(
    rpc: JsonRpcRequest,
    msg: string,
    status: number,
  ): McpHttpResponse {
    return writeResult(
      rpc.id,
      this.stampResultEnvelope(errorResultBlock(msg)),
      status,
    );
  }
}

/**
 * Reduces all occurrences of a header to one value for comparison
 * against the body. Sent once, or several times with byte-identical
 * values (benign proxy duplication), resolves to that value. Sent
 * several times with differing values is a validation failure in its
 * own right — conflicting duplicates are exactly the
 * multiple-sources-of-truth hazard header/body validation closes — so
 * ok=false and the caller rejects without comparing anything.
 */
export function singleHeaderValue(
  req: McpHttpRequest,
  name: string,
): { value: string; ok: boolean } {
  const vals = headerValues(req, name);
  if (vals.length === 0) return { value: '', ok: true };
  if (vals.length === 1) return { value: vals[0], ok: true };
  for (const v of vals.slice(1)) {
    if (v !== vals[0]) return { value: '', ok: false };
  }
  return { value: vals[0], ok: true };
}

/**
 * V2's id rule: a JSON string, or a JSON number with no fractional
 * part. Base JSON-RPC also allows null, but this revision forbids it;
 * boolean, object, and array ids are rejected the same way.
 */
export function validModernRequestId(id: RawJSON): boolean {
  const raw = id.raw.trim();
  if (raw === 'null') return false;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return false;
  }
  if (typeof parsed === 'string') return true;
  if (typeof parsed === 'number') return Number.isInteger(parsed);
  return false;
}

/**
 * V3: decodes the reserved params._meta keys. protocolVersion and
 * clientCapabilities are required; clientInfo is optional. A missing
 * or non-object params/_meta fails the same way as missing keys.
 */
export function parseModernMeta(
  params: unknown,
): { meta: ModernRequestMeta } | { error: CheckError } {
  const fail = (msg: string): { error: CheckError } => ({
    error: { code: MCP_ERR_INVALID_PARAMS, msg, status: 400 },
  });

  if (params === undefined || params === null || typeof params !== 'object') {
    return fail('missing required params._meta');
  }
  const metaRaw = (params as Record<string, unknown>)._meta;
  if (metaRaw === undefined || metaRaw === null || typeof metaRaw !== 'object' || Array.isArray(metaRaw)) {
    return fail('missing required params._meta');
  }
  const metaObj = metaRaw as Record<string, unknown>;
  if (!(META_KEY_PROTOCOL_VERSION in metaObj)) {
    return fail(`missing required _meta key: ${META_KEY_PROTOCOL_VERSION}`);
  }
  if (!(META_KEY_CLIENT_CAPABILITIES in metaObj)) {
    return fail(`missing required _meta key: ${META_KEY_CLIENT_CAPABILITIES}`);
  }

  const verRaw = metaObj[META_KEY_PROTOCOL_VERSION];
  const meta: ModernRequestMeta = {
    version: typeof verRaw === 'string' ? verRaw : '',
    versionIsText: typeof verRaw === 'string',
    versionRaw: verRaw,
    hasClientInfo: false,
  };

  // clientInfo only feeds audit metadata; a value that is not an
  // object is treated as absent rather than rejected, since V3 does
  // not require the key at all.
  const ci = metaObj[META_KEY_CLIENT_INFO];
  if (ci !== undefined && ci !== null && typeof ci === 'object' && !Array.isArray(ci)) {
    const obj = ci as Record<string, unknown>;
    meta.hasClientInfo = true;
    meta.clientName = typeof obj.name === 'string' ? obj.name : '';
    meta.clientVersion = typeof obj.version === 'string' ? obj.version : '';
  }
  return { meta };
}

/**
 * Peeks params.name out of a tools/call body without requiring the
 * rest of params to decode, so V7 can run independently of whatever
 * else is wrong with the body. `present` reports whether params is an
 * object carrying a "name" key at all; `isString` distinguishes a
 * non-string value, which V7 defers to V9 rather than treating as
 * absent.
 */
export function rawToolCallName(params: unknown): {
  name: string;
  present: boolean;
  isString: boolean;
} {
  if (params === undefined || params === null || typeof params !== 'object' || Array.isArray(params)) {
    return { name: '', present: false, isString: false };
  }
  const obj = params as Record<string, unknown>;
  if (!('name' in obj)) return { name: '', present: false, isString: false };
  const v = obj.name;
  if (typeof v !== 'string') return { name: '', present: true, isString: false };
  return { name: v, present: true, isString: true };
}

/**
 * Decodes a header value that may carry the Base64 sentinel encoding.
 * A value that is not sentinel-wrapped is returned unchanged.
 * A wrapped value that fails to decode returns undefined so the
 * caller fails closed: the spec requires decoding before comparing,
 * and a malformed encoding can never legitimately match the body.
 * A value that merely looks like the sentinel is always treated as
 * encoded, per spec.
 */
export function decodeMcpSentinel(v: string): string | undefined {
  if (!v.startsWith(SENTINEL_PREFIX) || !v.endsWith(SENTINEL_SUFFIX)) {
    return v;
  }
  const inner = v.slice(SENTINEL_PREFIX.length, v.length - SENTINEL_SUFFIX.length);
  // Reject anything that is not strict, canonical standard base64:
  // Go's base64.StdEncoding.DecodeString errors on bad padding and
  // non-alphabet bytes, whereas Buffer.from is lenient.
  if (!/^[A-Za-z0-9+/]*={0,2}$/.test(inner) || inner.length % 4 !== 0) {
    return undefined;
  }
  const buf = Buffer.from(inner, 'base64');
  if (buf.toString('base64') !== inner) return undefined;
  return buf.toString('utf8');
}

/**
 * The Meta.extra audit bag for a modern invocation: the spec version
 * always, and the client identity when the request carried clientInfo.
 */
export function modernInvocationExtra(
  meta: ModernRequestMeta,
): Record<string, string> {
  const extra: Record<string, string> = {
    mcp_spec_version: MCP_SPEC_2026_07_28,
  };
  if (meta.hasClientInfo) {
    extra.mcp_client_name = meta.clientName ?? '';
    extra.mcp_client_version = meta.clientVersion ?? '';
  }
  return extra;
}

/**
 * Renders a decoded JSON value the way Go's %v verb does, used in the
 * V4 mismatch message where the _meta value may be of any JSON type.
 */
function goFormatValue(v: unknown): string {
  if (v === null || v === undefined) return '<nil>';
  if (typeof v === 'string') return v;
  if (typeof v === 'boolean') return v ? 'true' : 'false';
  if (typeof v === 'number') return String(v);
  return JSON.stringify(v);
}

export { DestructiveBlockedError };
