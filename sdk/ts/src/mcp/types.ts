/**
 * @module mcp/types
 * @package @hop-top/kit
 *
 * Shared vocabulary for the dual-spec MCP surface: the bridge
 * abstraction the surface invokes through, the safety gate, the
 * mount options, and the canonical JSON encoder that makes wire
 * output byte-identical to the Go reference.
 *
 * Protocol constants are taken from the official v2 SDK
 * (@modelcontextprotocol/server) rather than re-declared here, so a
 * reserved key or spec error code can never drift from upstream. The
 * v1 package (@modelcontextprotocol/sdk) is legacy-era only and
 * cannot serve 2026-07-28 — it is deliberately not a dependency.
 */

import {
  CLIENT_CAPABILITIES_META_KEY,
  CLIENT_INFO_META_KEY,
  PROTOCOL_VERSION_META_KEY,
  ProtocolErrorCode,
  SERVER_INFO_META_KEY,
} from '@modelcontextprotocol/server';

// --- protocol constants -------------------------------------------------

/** Reserved `params._meta` / result `_meta` keys (from the v2 SDK). */
export const META_KEY_PROTOCOL_VERSION = PROTOCOL_VERSION_META_KEY;
export const META_KEY_CLIENT_INFO = CLIENT_INFO_META_KEY;
export const META_KEY_CLIENT_CAPABILITIES = CLIENT_CAPABILITIES_META_KEY;
export const META_KEY_SERVER_INFO = SERVER_INFO_META_KEY;

/** Header names carrying the modern routing signals. */
export const HEADER_MCP_PROTOCOL_VERSION = 'MCP-Protocol-Version';
export const HEADER_MCP_METHOD = 'Mcp-Method';
export const HEADER_MCP_NAME = 'Mcp-Name';

/**
 * JSON-RPC error codes. The base codes and the two spec-reserved
 * 2026-07-28 additions come from the SDK's ProtocolErrorCode;
 * HeaderMismatch (-32020) is spec-reserved but absent from the SDK
 * enum, so it is pinned here.
 */
export const MCP_ERR_PARSE = ProtocolErrorCode.ParseError;
export const MCP_ERR_INVALID_REQUEST = ProtocolErrorCode.InvalidRequest;
export const MCP_ERR_METHOD_NOT_FOUND = ProtocolErrorCode.MethodNotFound;
export const MCP_ERR_INVALID_PARAMS = ProtocolErrorCode.InvalidParams;
export const MCP_ERR_INTERNAL = ProtocolErrorCode.InternalError;
export const MCP_ERR_HEADER_MISMATCH = -32020;
export const MCP_ERR_MISSING_CLIENT_CAPABILITY =
  ProtocolErrorCode.MissingRequiredClientCapability;
export const MCP_ERR_UNSUPPORTED_VERSION =
  ProtocolErrorCode.UnsupportedProtocolVersion;

/** The two protocol revisions this surface serves. */
export const MCP_SPEC_2024_11_05 = '2024-11-05';
export const MCP_SPEC_2026_07_28 = '2026-07-28';

export type McpSpecVersion =
  | typeof MCP_SPEC_2024_11_05
  | typeof MCP_SPEC_2026_07_28;

/** resultType discriminator values on modern result envelopes. */
export const RESULT_TYPE_COMPLETE = 'complete';
export const RESULT_TYPE_INPUT_REQUIRED = 'input_required';

/** cacheScope values on modern cacheable list results. */
export type McpCacheScope = 'public' | 'private';

/** Default server identity, matching Go's cmdsurface defaults. */
export const DEFAULT_MCP_SERVER_NAME = 'cmdsurface';
export const DEFAULT_MCP_SERVER_VERSION = '0.0.0';
export const DEFAULT_MCP_PATH = '/mcp';

// --- safety gate --------------------------------------------------------

/**
 * Surfaces a leaf may be invoked through. Mirrors Go's Surface type;
 * both MCP spec revisions share the single "mcp" surface — one
 * mount, one exposure decision.
 */
export type Surface = 'cli' | 'lib' | 'mcp' | 'rest' | 'rpc' | 'ws' | 'bus';

export const SURFACE_CLI: Surface = 'cli';
export const SURFACE_LIB: Surface = 'lib';
export const SURFACE_MCP: Surface = 'mcp';

/**
 * SafetyClass is the bridge's read of a leaf's safety annotations —
 * the input the policy gate consults. Mirrors Go's SafetyClass.
 */
export interface SafetyClass {
  /** Set for the destructive kit/side-effect tiers. */
  destructive?: boolean;
  /** Set when kit/auth-required is "true". */
  authRequired?: boolean;
  /** Set when kit/requires-confirmation is "true". */
  requiresConfirmation?: boolean;
  permissions?: string[];
  exitCodes?: string[];
}

/**
 * Policy gates which Surface may invoke a leaf of a given
 * SafetyClass. Ported from Go's cmdsurface Policy — NOT from
 * src/safety.ts, which implements the unrelated Factor 10 CLI
 * `--force` guard and has no bearing on surface exposure.
 */
export interface Policy {
  /**
   * Surfaces on which destructive leaves MAY be invoked. CLI and Lib
   * are always allowed regardless. Empty = block all remote
   * destructive invocation (the default).
   */
  allowDestructiveOn?: Surface[];
  /** Surfaces a leaf is exposed on when its config omits `enabled`. */
  defaultEnabled?: Surface[];
}

/**
 * The conservative default policy: no remote surface may invoke a
 * destructive command; default enablement is CLI + Lib + MCP.
 * `allowDestructiveOn` is deliberately empty — empty means block-all,
 * not allow-all.
 */
export function defaultPolicy(): Policy {
  return {
    allowDestructiveOn: [],
    defaultEnabled: [SURFACE_CLI, SURFACE_LIB, SURFACE_MCP],
  };
}

/**
 * Reports whether `cls` may be invoked via surface `s` under `p`.
 *
 *  1. cli and lib are always allowed (local runtime).
 *  2. Non-destructive commands are allowed on every other surface.
 *  3. Destructive commands are allowed only when `s` is listed in
 *     `allowDestructiveOn`.
 *
 * Surface enablement (per-leaf opt-in) is gated separately; this
 * enforces only the destructive ceiling.
 */
export function policyAllowed(
  p: Policy,
  cls: SafetyClass,
  s: Surface,
): boolean {
  if (s === SURFACE_CLI || s === SURFACE_LIB) return true;
  if (!cls.destructive) return true;
  return (p.allowDestructiveOn ?? []).includes(s);
}

/** Error a bridge raises when the policy gate blocks a leaf. */
export class DestructiveBlockedError extends Error {
  constructor(pathLabel: string, surface: Surface) {
    super(
      `cmdsurface: destructive command blocked on this surface: ${pathLabel} on ${surface}`,
    );
    this.name = 'DestructiveBlockedError';
  }
}

/** Error a bridge raises when a leaf path resolves to nothing. */
export class UnknownCommandError extends Error {
  constructor(message = 'unknown command') {
    super(message);
    this.name = 'UnknownCommandError';
  }
}

/** Error a bridge raises when a leaf is not exposed on the surface. */
export class SurfaceNotEnabledError extends Error {
  constructor(message = 'surface not enabled') {
    super(message);
    this.name = 'SurfaceNotEnabledError';
  }
}

// --- bridge -------------------------------------------------------------

/** A JSON Schema property object describing one flag. */
export interface FlagSchema {
  type: string;
  description: string;
  items?: { type: string };
}

/**
 * One invocable leaf command exposed as an MCP tool. `path` is the
 * leaf's command path; the MCP tool name is the dotted join.
 */
export interface Leaf {
  /** Command path, e.g. ["widget","add"] → tool "widget.add". */
  path: string[];
  /** Short description, used as the tool description. */
  description: string;
  /** JSON Schema properties derived from the leaf's flags. */
  properties: Record<string, FlagSchema>;
  /** Names of required flags, in declaration order. */
  required?: string[];
  /** Safety annotations consulted by the policy gate. */
  class?: SafetyClass;
  /** Surfaces this leaf is exposed on. */
  enabled?: Partial<Record<Surface, boolean>>;
}

/** Metadata attached to one invocation, forwarded to audit sinks. */
export interface InvocationMeta {
  surface: Surface;
  requestedAt: Date;
  /** Free-form audit bag; the modern path records spec + client info. */
  extra?: Record<string, string>;
}

/** One leaf invocation. */
export interface Invocation {
  path: string[];
  flags?: Record<string, unknown>;
  meta: InvocationMeta;
}

/** The result of invoking a leaf. */
export interface InvokeResult {
  stdout: string;
  stderr?: string;
  exitCode: number;
  /** Structured payload; becomes structuredContent on the modern path. */
  data?: unknown;
}

/**
 * The command-tree abstraction the MCP surface serves. Adopters
 * implement this over their own command framework (see
 * `commanderBridge` for a commander adapter); the surface itself
 * never touches commander, so it stays framework-free.
 */
export interface McpBridge {
  /** Every leaf known to the bridge, in stable enumeration order. */
  leaves(): Leaf[];
  /**
   * Invokes one leaf. MUST apply the policy gate and throw
   * DestructiveBlockedError when it blocks, so both eras inherit the
   * identical destructive ceiling from one code path.
   */
  invoke(inv: Invocation): Promise<InvokeResult> | InvokeResult;
}

// --- mount options ------------------------------------------------------

/**
 * Mount options, mirroring Go's MountMCP option set. Defaults match
 * Go exactly: both eras, path "/mcp", empty origin allowlist, cache
 * hints ttlMs=0 / cacheScope="private".
 */
export interface McpMountOptions {
  /** Mount path. Default "/mcp". */
  path?: string;
  /** Server identity in serverInfo. */
  serverInfo?: { name: string; version: string };
  /**
   * Which eras this mount serves. Absent = both. An explicit empty
   * array, or an unrecognized version, is a mount-time error.
   */
  specVersions?: McpSpecVersion[];
  /** ttlMs + cacheScope on modern cacheable list results. */
  cacheHints?: { ttlMs?: number; cacheScope?: McpCacheScope };
  /** Exact-match Origin allowlist; absent = no Origin check. */
  originAllowlist?: string[];
  /** HMAC key enabling the MRTR confirmation flow. Must be non-empty. */
  confirmationKey?: Uint8Array | string;
  /** Policy gate. Default: defaultPolicy(). */
  policy?: Policy;
}

/** Resolved, validated mount configuration. */
export interface ResolvedMcpConfig {
  path: string;
  serverName: string;
  serverVersion: string;
  legacyEnabled: boolean;
  modernEnabled: boolean;
  cacheTtlMs: number;
  cacheScope: McpCacheScope;
  originAllowlist: string[];
  confirmationKey?: Uint8Array;
  policy: Policy;
}

/**
 * Validates and resolves mount options. Reproduces Go's two
 * mount-time errors rather than silently absorbing them: an explicit
 * empty spec-version set, and a negative ttl or unrecognized scope.
 */
export function resolveMcpConfig(
  opts: McpMountOptions = {},
): ResolvedMcpConfig {
  let legacyEnabled = true;
  let modernEnabled = true;

  if (opts.specVersions !== undefined) {
    if (opts.specVersions.length === 0) {
      throw new Error(
        'cmdsurface: specVersions: at least one spec version required',
      );
    }
    legacyEnabled = false;
    modernEnabled = false;
    for (const v of opts.specVersions) {
      if (v === MCP_SPEC_2024_11_05) legacyEnabled = true;
      else if (v === MCP_SPEC_2026_07_28) modernEnabled = true;
      else {
        throw new Error(
          `cmdsurface: specVersions: unrecognized version "${v}"`,
        );
      }
    }
  }

  let cacheTtlMs = 0;
  let cacheScope: McpCacheScope = 'private';
  if (opts.cacheHints !== undefined) {
    const ttl = opts.cacheHints.ttlMs ?? 0;
    if (ttl < 0) {
      throw new Error('cmdsurface: cacheHints: negative ttl');
    }
    // Truncate to whole milliseconds, matching Go's
    // time.Duration.Milliseconds().
    cacheTtlMs = Math.trunc(ttl);
    const scope = opts.cacheHints.cacheScope ?? 'private';
    if (scope !== 'public' && scope !== 'private') {
      throw new Error(
        `cmdsurface: cacheHints: unknown cache scope ${String(scope)}`,
      );
    }
    cacheScope = scope;
  }

  let confirmationKey: Uint8Array | undefined;
  if (opts.confirmationKey !== undefined) {
    const key =
      typeof opts.confirmationKey === 'string'
        ? new TextEncoder().encode(opts.confirmationKey)
        : opts.confirmationKey;
    if (key.length === 0) {
      throw new Error('cmdsurface: confirmationKey: empty key');
    }
    confirmationKey = key;
  }

  return {
    path: opts.path ?? DEFAULT_MCP_PATH,
    serverName: opts.serverInfo?.name ?? DEFAULT_MCP_SERVER_NAME,
    serverVersion: opts.serverInfo?.version ?? DEFAULT_MCP_SERVER_VERSION,
    legacyEnabled,
    modernEnabled,
    cacheTtlMs,
    cacheScope,
    originAllowlist: opts.originAllowlist ?? [],
    confirmationKey,
    policy: opts.policy ?? defaultPolicy(),
  };
}

// --- tool naming --------------------------------------------------------

/** Renders a leaf path as a dotted MCP tool name. */
export function toolName(path: string[]): string {
  return path.join('.');
}

/** Splits a dotted MCP tool name back into a leaf path. */
export function pathFromToolName(name: string): string[] {
  if (name === '') return [];
  return name.split('.');
}

/** Renders one leaf as an MCP tool descriptor (shared by both eras). */
export function buildToolEnvelope(leaf: Leaf): Record<string, unknown> {
  const schema: Record<string, unknown> = {
    type: 'object',
    properties: leaf.properties ?? {},
  };
  if (leaf.required && leaf.required.length > 0) {
    schema.required = leaf.required;
  }
  return {
    name: toolName(leaf.path),
    description: leaf.description,
    inputSchema: schema,
  };
}

/**
 * Renders a bridge result as the tools/call content block list.
 * Shared verbatim by both eras so content layout cannot drift:
 * stdout text block, "[stderr] " block when present, JSON text block
 * when `data` is present.
 */
export function renderCallResult(
  res: InvokeResult,
): Record<string, unknown> {
  const content: Array<Record<string, unknown>> = [
    { type: 'text', text: res.stdout },
  ];
  if (res.stderr) {
    content.push({ type: 'text', text: `[stderr] ${res.stderr}` });
  }
  if (res.data !== undefined && res.data !== null) {
    content.push({ type: 'text', text: canonicalJSONStringify(res.data) });
  }
  return { content, isError: res.exitCode !== 0 };
}

/** A tools/call result envelope flagged isError with one text block. */
export function errorResultBlock(msg: string): Record<string, unknown> {
  return {
    content: [{ type: 'text', text: msg }],
    isError: true,
  };
}

// --- canonical JSON -----------------------------------------------------

/**
 * A pre-serialized JSON fragment emitted verbatim. Used to echo a
 * request `id` back exactly as received (including `null`, which Go
 * round-trips byte-for-byte through json.RawMessage).
 */
export class RawJSON {
  constructor(readonly raw: string) {}
}

/**
 * Serializes a value the way Go's encoding/json does for the shapes
 * this surface emits, so wire output is byte-identical to the Go
 * reference rather than merely structurally equal — the fixtures
 * compare bytes, with no decode/re-encode step.
 *
 * Go's rule is not "sort everything": `encoding/json` emits STRUCT
 * fields in declaration order and MAP keys in sorted order. This
 * surface builds result bodies as map[string]any (sorted) inside a
 * jsonRPCResponse struct (declaration order), so the encoder mirrors
 * exactly that split: plain objects are sorted, and the response
 * envelope is emitted through `encodeEnvelope` below, which pins
 * field order. Sorting is by UTF-16 code unit (the JS default),
 * which agrees with Go's byte-wise sort over the ASCII keys emitted
 * here.
 */
export function canonicalJSONStringify(value: unknown): string {
  if (value instanceof RawJSON) return value.raw;
  if (value === null || typeof value !== 'object') {
    return JSON.stringify(value) as string;
  }
  if (Array.isArray(value)) {
    return `[${value.map(canonicalJSONStringify).join(',')}]`;
  }
  const src = value as Record<string, unknown>;
  const parts: string[] = [];
  for (const key of Object.keys(src).sort()) {
    const v = src[key];
    if (v === undefined) continue;
    parts.push(`${JSON.stringify(key)}:${canonicalJSONStringify(v)}`);
  }
  return `{${parts.join(',')}}`;
}

/** A JSON-RPC error object, emitted in Go struct-field order. */
export interface JsonRpcErrorObject {
  code: number;
  message: string;
  data?: unknown;
}

/**
 * Encodes one JSON-RPC response envelope with Go's struct field
 * order — `jsonrpc`, `id`, then `result` or `error` — and a trailing
 * newline, matching json.Encoder.Encode. `id` is omitted entirely
 * when absent (Go's `omitempty` on a nil json.RawMessage) but
 * emitted as `null` when the request carried an explicit null id.
 */
export function encodeEnvelope(env: {
  id?: RawJSON;
  result?: unknown;
  error?: JsonRpcErrorObject;
}): string {
  const parts: string[] = ['"jsonrpc":"2.0"'];
  if (env.id !== undefined) {
    parts.push(`"id":${env.id.raw}`);
  }
  if (env.error !== undefined) {
    const errParts: string[] = [
      `"code":${JSON.stringify(env.error.code)}`,
      `"message":${JSON.stringify(env.error.message)}`,
    ];
    if (env.error.data !== undefined) {
      errParts.push(`"data":${canonicalJSONStringify(env.error.data)}`);
    }
    parts.push(`"error":{${errParts.join(',')}}`);
  } else if (env.result !== undefined) {
    parts.push(`"result":${canonicalJSONStringify(env.result)}`);
  }
  return `{${parts.join(',')}}\n`;
}
