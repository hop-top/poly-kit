/**
 * @module mcp/identity
 * @package @hop-top/kit
 *
 * Who is calling, as both eras' `tools/call` gate reads it. The only
 * source of identity is the mount's verifier: an `Authorization`
 * header is presence, not verification, and a caller or scopes the
 * request claims never become identity. Mirrors the Go surfaces,
 * where only an established `Meta` admits a `kit/auth-required` leaf.
 */

import type { McpHttpRequest } from './dispatch.js';
import {
  SURFACE_MCP,
  type InvocationMeta,
  type McpIdentity,
  type McpVerifier,
} from './types.js';

/** The key the verifier's scopes are recorded under in `InvocationMeta.extra`. */
export const SCOPES_EXTRA_KEY = 'scopes';

/**
 * Runs the mount's verifier over `req`. Returns the identity it
 * established, or `undefined` when there is no verifier, it refused
 * (`null`/`undefined`), returned something that is not an object, or
 * threw: every failure fails closed.
 */
export async function establishCaller(
  verifier: McpVerifier | undefined,
  req: McpHttpRequest,
): Promise<McpIdentity | undefined> {
  if (verifier === undefined) return undefined;
  let ident: McpIdentity | null | undefined;
  try {
    ident = await verifier(req);
  } catch {
    return undefined;
  }
  if (ident === null || typeof ident !== 'object') return undefined;
  return ident;
}

/**
 * Builds one `tools/call` invocation's meta: the MCP surface, the
 * request time, the audit `extra` the era supplies, and the identity
 * the verifier established — caller, tenant, `established`, and the
 * comma-joined `scopes` extra entry. Nothing from `ident` is set when
 * it is `undefined`, and nothing here reads the request.
 */
export function invocationMeta(
  ident: McpIdentity | undefined,
  extra?: Record<string, string>,
): InvocationMeta {
  const meta: InvocationMeta = { surface: SURFACE_MCP, requestedAt: new Date() };
  const bag: Record<string, string> = { ...(extra ?? {}) };
  delete bag[SCOPES_EXTRA_KEY];
  if (ident !== undefined) {
    meta.established = 'verified';
    if (typeof ident.caller === 'string' && ident.caller !== '') {
      meta.caller = ident.caller;
    }
    if (typeof ident.tenant === 'string' && ident.tenant !== '') {
      meta.tenant = ident.tenant;
    }
    if (Array.isArray(ident.scopes) && ident.scopes.length > 0) {
      bag[SCOPES_EXTRA_KEY] = ident.scopes.join(',');
    }
  }
  if (Object.keys(bag).length > 0) meta.extra = bag;
  return meta;
}
