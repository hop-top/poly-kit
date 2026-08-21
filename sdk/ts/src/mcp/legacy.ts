/**
 * @module mcp/legacy
 * @package @hop-top/kit
 *
 * The 2024-11-05 MCP handler: `initialize`, `tools/list`,
 * `tools/call`, plain JSON-RPC over one POST path, no sessions, no
 * SSE.
 *
 * This module is byte-for-byte frozen. ADR 0042's hard invariant is
 * "2024-11-05 behavior is preserved byte-for-byte; additive only; no
 * deprecation", and the three-way module split (legacy / modern /
 * dispatch) exists precisely so a reviewer can confirm a modern-era
 * change did not touch this file. Modern-era behavior belongs in
 * modern.ts — never here.
 */

import {
  errorResultBlock,
  encodeEnvelope,
  buildToolEnvelope,
  MCP_ERR_INVALID_PARAMS,
  MCP_ERR_INVALID_REQUEST,
  MCP_ERR_METHOD_NOT_FOUND,
  MCP_ERR_PARSE,
  MCP_SPEC_2024_11_05,
  pathFromToolName,
  renderCallResult,
  SURFACE_MCP,
  type JsonRpcErrorObject,
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
import type { JsonRpcRequest, McpHttpRequest, McpHttpResponse } from './dispatch.js';
import { jsonResponse } from './dispatch.js';

/** Writes a successful JSON-RPC envelope. */
export function writeResult(
  id: RawJSON | undefined,
  result: unknown,
  status: number,
): McpHttpResponse {
  return jsonResponse(status, encodeEnvelope({ id, result }));
}

/** Writes a JSON-RPC error envelope. */
export function writeError(
  id: RawJSON | undefined,
  error: JsonRpcErrorObject,
  status: number,
): McpHttpResponse {
  return jsonResponse(status, encodeEnvelope({ id, error }));
}

/**
 * Renders the -32700 parse error exactly as Go does, including the
 * encoding/json message text the fixtures pin. Go reports the offset
 * of the first offending byte in the phrase "invalid character %q
 * looking for beginning of object key string" when an object key is
 * expected; other positions produce different wording. Only the
 * shapes this surface can actually receive are modelled.
 */
export function parseErrorResponse(body: string): McpHttpResponse {
  return writeError(
    undefined,
    { code: MCP_ERR_PARSE, message: `parse error: ${goJSONParseMessage(body)}` },
    400,
  );
}

/**
 * Reproduces Go's encoding/json syntax-error text for a body this
 * surface failed to parse. Go's decoder reports the first character
 * that cannot begin the token it expects; for a top-level object
 * whose first key is unquoted (the fixture case `{not json`) that is
 * "invalid character 'n' looking for beginning of object key string".
 */
function goJSONParseMessage(body: string): string {
  const trimmed = body.replace(/^[\s]*/, '');
  if (trimmed.startsWith('{')) {
    // Scan past the opening brace and any whitespace to the first
    // character of what must be a quoted object key.
    const rest = trimmed.slice(1).replace(/^[\s]*/, '');
    const ch = rest[0];
    if (ch !== undefined && ch !== '"' && ch !== '}') {
      return `invalid character '${ch}' looking for beginning of object key string`;
    }
  }
  const ch = trimmed[0];
  if (ch !== undefined) {
    return `invalid character '${ch}' looking for beginning of value`;
  }
  return 'unexpected end of JSON input';
}

/**
 * The 2024-11-05 handler. Stateless across requests; holds only
 * immutable mount configuration and the bridge.
 */
export class LegacyMcpHandler {
  constructor(
    private readonly bridge: McpBridge,
    private readonly cfg: ResolvedMcpConfig,
  ) {}

  /**
   * Routes one already-parsed JSON-RPC request by method. An unknown
   * method returns -32601 at HTTP 200 — today's behavior, preserved
   * deliberately (the modern handler uses 404 instead; the asymmetry
   * is required by byte-for-byte preservation).
   */
  async serve(
    req: McpHttpRequest,
    rpc: JsonRpcRequest,
  ): Promise<McpHttpResponse> {
    if (rpc.jsonrpc !== undefined && rpc.jsonrpc !== '2.0') {
      return writeError(
        rpc.id,
        { code: MCP_ERR_INVALID_REQUEST, message: 'invalid jsonrpc version' },
        400,
      );
    }

    switch (rpc.method) {
      case 'initialize':
        return this.handleInitialize(rpc);
      case 'tools/list':
        return this.handleToolsList(rpc);
      case 'tools/call':
        return this.handleToolsCall(req, rpc);
      default:
        return writeError(
          rpc.id,
          {
            code: MCP_ERR_METHOD_NOT_FOUND,
            message: `method not found: ${rpc.method}`,
          },
          200,
        );
    }
  }

  /** The minimal initialize response: version, capabilities, identity. */
  private handleInitialize(rpc: JsonRpcRequest): McpHttpResponse {
    return writeResult(
      rpc.id,
      {
        protocolVersion: MCP_SPEC_2024_11_05,
        capabilities: { tools: {} },
        serverInfo: {
          name: this.cfg.serverName,
          version: this.cfg.serverVersion,
        },
      },
      200,
    );
  }

  /** One tool envelope per leaf exposed on the MCP surface. */
  private handleToolsList(rpc: JsonRpcRequest): McpHttpResponse {
    return writeResult(rpc.id, { tools: this.exposedTools() }, 200);
  }

  private exposedTools(): Array<Record<string, unknown>> {
    const tools: Array<Record<string, unknown>> = [];
    for (const leaf of this.bridge.leaves()) {
      if (!isExposed(leaf)) continue;
      tools.push(buildToolEnvelope(leaf));
    }
    return tools;
  }

  /**
   * Resolves the leaf, applies the pre-flight auth + confirmation
   * gates, and dispatches through the bridge. Unknown / not-enabled
   * tools are JSON-RPC errors; every other failure (bridge errors,
   * destructive blocks, non-zero exit codes) is an isError result.
   */
  private async handleToolsCall(
    req: McpHttpRequest,
    rpc: JsonRpcRequest,
  ): Promise<McpHttpResponse> {
    const params = (rpc.params ?? {}) as {
      name?: unknown;
      arguments?: Record<string, unknown>;
    };
    const name = typeof params.name === 'string' ? params.name : '';
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

    // Auth + confirmation gating, mirrored onto the result envelope so
    // MCP-aware clients see isError while HTTP-only clients see the
    // matching status code.
    if (leaf.class?.authRequired && !headerValue(req, 'authorization')) {
      return writeResult(
        rpc.id,
        errorResultBlock('authentication required'),
        401,
      );
    }
    if (
      leaf.class?.requiresConfirmation &&
      !headerValue(req, 'x-confirm-token')
    ) {
      return writeResult(
        rpc.id,
        errorResultBlock('confirmation required'),
        428,
      );
    }

    try {
      const res = await this.bridge.invoke({
        path: [...leaf.path],
        flags: params.arguments,
        meta: { surface: SURFACE_MCP, requestedAt: new Date() },
      });
      return writeResult(rpc.id, renderCallResult(res), 200);
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
      // DestructiveBlockedError and every other invoke failure render
      // as an isError result at HTTP 200.
      return writeResult(
        rpc.id,
        errorResultBlock(errorMessage(err)),
        200,
      );
    }
  }
}

/** Reports whether a leaf is exposed on the MCP surface. */
export function isExposed(leaf: Leaf): boolean {
  // Absent `enabled` means the bridge applies its policy defaults,
  // which include the MCP surface; an explicit false hides the leaf.
  return leaf.enabled?.[SURFACE_MCP] !== false;
}

/** Resolves a dotted tool name to a leaf, or undefined. */
export function resolveLeaf(
  bridge: McpBridge,
  name: string,
): Leaf | undefined {
  const path = pathFromToolName(name);
  if (path.length === 0) return undefined;
  return bridge
    .leaves()
    .find(
      (l) =>
        l.path.length === path.length &&
        l.path.every((seg, i) => seg === path[i]),
    );
}

/** Case-insensitive single header lookup. */
export function headerValue(
  req: McpHttpRequest,
  name: string,
): string | undefined {
  const vals = headerValues(req, name);
  return vals.length > 0 ? vals[0] : undefined;
}

/** All values for a header name, matched case-insensitively. */
export function headerValues(
  req: McpHttpRequest,
  name: string,
): string[] {
  const want = name.toLowerCase();
  const out: string[] = [];
  for (const [k, v] of Object.entries(req.headers ?? {})) {
    if (k.toLowerCase() !== want) continue;
    if (Array.isArray(v)) out.push(...v);
    else if (v !== undefined) out.push(v);
  }
  return out;
}

/** Extracts an error message the way Go's err.Error() renders it. */
export function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

export { DestructiveBlockedError };
