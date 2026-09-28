/**
 * The kit/auth-required gate on both eras: only the mount's verifier
 * establishes a caller. An Authorization header is presence, not
 * verification, and a caller or scopes the request claims never
 * become identity.
 */

import { describe, expect, it } from 'vitest';

import { createMcpHandler, type McpHttpRequest } from './dispatch.js';
import { headerValue } from './legacy.js';
import { legacyLockBridge, modernLockBridge } from './testtree.js';
import type {
  Invocation,
  McpBridge,
  McpMountOptions,
  McpVerifier,
} from './types.js';

const MODERN_META = {
  'io.modelcontextprotocol/clientCapabilities': {},
  'io.modelcontextprotocol/protocolVersion': '2026-07-28',
};

/** A bridge that records every invocation it runs. */
function recording(inner: McpBridge): { bridge: McpBridge; calls: Invocation[] } {
  const calls: Invocation[] = [];
  return {
    calls,
    bridge: {
      leaves: () => inner.leaves(),
      invoke(inv) {
        calls.push(inv);
        return inner.invoke(inv);
      },
    },
  };
}

/** Accepts exactly `Bearer good` as alice in acme with two scopes. */
const verifyGood: McpVerifier = (req) =>
  headerValue(req, 'authorization') === 'Bearer good'
    ? { caller: 'alice', tenant: 'acme', scopes: ['read', 'write'] }
    : null;

type Era = 'legacy' | 'modern';

function call(
  era: Era,
  name: string,
  headers: Record<string, string> = {},
  extraParams: Record<string, unknown> = {},
): McpHttpRequest {
  if (era === 'legacy') {
    return {
      method: 'POST',
      headers,
      body: JSON.stringify({
        jsonrpc: '2.0',
        id: 1,
        method: 'tools/call',
        params: { name, ...extraParams },
      }),
    };
  }
  const { _meta: claimed, ...rest } = extraParams as { _meta?: object };
  return {
    method: 'POST',
    headers: {
      'MCP-Protocol-Version': '2026-07-28',
      'Mcp-Method': 'tools/call',
      'Mcp-Name': name,
      ...headers,
    },
    body: JSON.stringify({
      jsonrpc: '2.0',
      id: 1,
      method: 'tools/call',
      params: { name, ...rest, _meta: { ...MODERN_META, ...(claimed ?? {}) } },
    }),
  };
}

function mount(era: Era, opts: McpMountOptions = {}) {
  const rec = recording(era === 'legacy' ? legacyLockBridge() : modernLockBridge());
  return { handler: createMcpHandler(rec.bridge, opts), calls: rec.calls };
}

const CLAIMS = { caller: 'admin', tenant: 'root', scopes: ['admin'] };

for (const era of ['legacy', 'modern'] as const) {
  describe(`${era} tools/call auth gate`, () => {
    it('refuses an auth-required leaf on a bare Authorization header', async () => {
      const { handler, calls } = mount(era);
      const res = await handler(
        call(era, 'secret', { Authorization: 'Bearer anything' }, { _meta: CLAIMS }),
      );
      expect(res.status).toBe(401);
      expect(res.headers['WWW-Authenticate']).toBe('Bearer');
      expect(res.body).toContain('"text":"authentication required"');
      expect(res.body).toContain('"isError":true');
      expect(calls).toHaveLength(0);
    });

    it('refuses when the verifier refuses the credential', async () => {
      const { handler, calls } = mount(era, { verifier: verifyGood });
      const res = await handler(call(era, 'secret', { Authorization: 'Bearer bad' }));
      expect(res.status).toBe(401);
      expect(res.headers['WWW-Authenticate']).toBe('Bearer');
      expect(calls).toHaveLength(0);
    });

    it('refuses when the verifier throws', async () => {
      const { handler, calls } = mount(era, {
        verifier: () => {
          throw new Error('verifier down');
        },
      });
      const res = await handler(call(era, 'secret', { Authorization: 'Bearer good' }));
      expect(res.status).toBe(401);
      expect(calls).toHaveLength(0);
    });

    it('admits once the verifier establishes the caller, async included', async () => {
      const { handler, calls } = mount(era, {
        verifier: async (req) => verifyGood(req),
      });
      const res = await handler(
        call(era, 'secret', { Authorization: 'Bearer good' }, { _meta: CLAIMS }),
      );
      expect(res.status).toBe(200);
      expect(res.body).toContain('"isError":false');
      expect(calls).toHaveLength(1);
      const meta = calls[0].meta;
      expect(meta.established).toBe('verified');
      expect(meta.caller).toBe('alice');
      expect(meta.tenant).toBe('acme');
      expect(meta.extra?.scopes).toBe('read,write');
    });

    it('never takes identity from the request on an open leaf', async () => {
      const { handler, calls } = mount(era);
      const res = await handler(
        call(era, 'ping', { Authorization: 'Bearer good' }, {
          _meta: CLAIMS,
          arguments: {},
        }),
      );
      expect(res.status).toBe(200);
      expect(calls).toHaveLength(1);
      const meta = calls[0].meta;
      expect(meta.established).toBeUndefined();
      expect(meta.caller).toBeUndefined();
      expect(meta.tenant).toBeUndefined();
      expect(meta.extra?.scopes).toBeUndefined();
    });

    it('runs an open leaf unestablished when the verifier refuses', async () => {
      const { handler, calls } = mount(era, { verifier: verifyGood });
      const res = await handler(call(era, 'ping', { Authorization: 'Bearer bad' }));
      expect(res.status).toBe(200);
      expect(calls[0].meta.established).toBeUndefined();
      expect(calls[0].meta.caller).toBeUndefined();
    });
  });
}
