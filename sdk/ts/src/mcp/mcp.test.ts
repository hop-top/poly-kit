/**
 * Unit coverage for behavior the 17 shared wire fixtures do not
 * reach: the era-detection edge cases in ADR 0042's worked table,
 * the modern validation chain V1-V9, mount-option refusals, cache
 * hints, the tasks-extension slot, and the safety gate itself.
 *
 * The fixtures remain the parity contract; these tests pin the rest
 * of the normative surface so a regression outside fixture coverage
 * still fails.
 */

import { describe, expect, it } from 'vitest';

import { createMcpHandler, detectMcpEra, parseJsonRpcRequest } from './dispatch.js';
import {
  decodeMcpSentinel,
  parseModernMeta,
  rawToolCallName,
  validModernRequestId,
} from './modern.js';
import { isTaskMethod, TASKS_SUPPORTED, TASK_METHODS } from './tasks.js';
import {
  canonicalJSONStringify,
  defaultPolicy,
  encodeEnvelope,
  policyAllowed,
  RawJSON,
  resolveMcpConfig,
  SURFACE_CLI,
  SURFACE_LIB,
  SURFACE_MCP,
  type McpBridge,
} from './types.js';
import { legacyLockBridge, modernLockBridge } from './testtree.js';

const MODERN_META = {
  'io.modelcontextprotocol/clientCapabilities': {},
  'io.modelcontextprotocol/protocolVersion': '2026-07-28',
};

const MODERN_HEADERS = (method: string, name?: string) => ({
  'MCP-Protocol-Version': '2026-07-28',
  'Mcp-Method': method,
  ...(name !== undefined ? { 'Mcp-Name': name } : {}),
});

function post(body: unknown, headers: Record<string, string | string[]> = {}) {
  return {
    method: 'POST',
    headers,
    body: typeof body === 'string' ? body : JSON.stringify(body),
  };
}

function parsed(body: unknown) {
  return parseJsonRpcRequest(
    typeof body === 'string' ? body : JSON.stringify(body),
  );
}

describe('era detection (ADR 0042 D1-D4, markers M1-M4)', () => {
  it('D2: initialize is legacy even with every modern marker present', () => {
    const req = post(
      { jsonrpc: '2.0', id: 1, method: 'initialize', params: { _meta: MODERN_META } },
      MODERN_HEADERS('initialize', 'x'),
    );
    expect(detectMcpEra(req, parsed(req.body))).toBe('legacy');
  });

  it('M4: server/discover is a marker on its own, with no headers', () => {
    const req = post({ jsonrpc: '2.0', id: 1, method: 'server/discover' });
    expect(detectMcpEra(req, parsed(req.body))).toBe('modern');
  });

  it('M1: a bare Mcp-Method header routes modern', () => {
    const req = post({ jsonrpc: '2.0', id: 1, method: 'tools/list' }, {
      'Mcp-Method': 'tools/list',
    });
    expect(detectMcpEra(req, parsed(req.body))).toBe('modern');
  });

  it('M2: a bare Mcp-Name header routes modern', () => {
    const req = post({ jsonrpc: '2.0', id: 1, method: 'tools/call' }, {
      'Mcp-Name': 'ping',
    });
    expect(detectMcpEra(req, parsed(req.body))).toBe('modern');
  });

  it('M3: the reserved protocolVersion key routes modern by key presence', () => {
    const req = post({
      jsonrpc: '2.0',
      id: 1,
      method: 'tools/list',
      // Value deliberately nonsense: detection tests key presence only.
      params: { _meta: { 'io.modelcontextprotocol/protocolVersion': 12345 } },
    });
    expect(detectMcpEra(req, parsed(req.body))).toBe('modern');
  });

  it('non-marker: bare params._meta (progressToken) stays legacy', () => {
    const req = post({
      jsonrpc: '2.0',
      id: 1,
      method: 'tools/list',
      params: { _meta: { progressToken: 'p1', traceparent: 'x' } },
    });
    expect(detectMcpEra(req, parsed(req.body))).toBe('legacy');
  });

  it('non-marker: MCP-Protocol-Version header stays legacy at any value', () => {
    for (const v of ['2024-11-05', '2025-06-18', '2026-07-28']) {
      const req = post({ jsonrpc: '2.0', id: 1, method: 'tools/list' }, {
        'MCP-Protocol-Version': v,
      });
      expect(detectMcpEra(req, parsed(req.body)), v).toBe('legacy');
    }
  });

  it('D4: an unknown bare method is legacy, answered -32601 at HTTP 200', async () => {
    const handler = createMcpHandler(legacyLockBridge());
    const res = await handler(post({ jsonrpc: '2.0', id: 9, method: 'nope' }));
    expect(res.status).toBe(200);
    expect(res.body).toBe(
      '{"jsonrpc":"2.0","id":9,"error":{"code":-32601,"message":"method not found: nope"}}\n',
    );
  });
});

describe('modern validation chain (V1-V9)', () => {
  const handler = () => createMcpHandler(modernLockBridge());

  it('V2: a notification (no id) with markers gets HTTP 202 and no body', async () => {
    const res = await handler()(
      post(
        { jsonrpc: '2.0', method: 'tools/list', params: { _meta: MODERN_META } },
        MODERN_HEADERS('tools/list'),
      ),
    );
    expect(res.status).toBe(202);
    expect(res.body).toBe('');
  });

  it('V2: an explicit null id with markers is -32600 at 400', async () => {
    const res = await handler()(
      post(
        { jsonrpc: '2.0', id: null, method: 'tools/list', params: { _meta: MODERN_META } },
        MODERN_HEADERS('tools/list'),
      ),
    );
    expect(res.status).toBe(400);
    expect(res.body).toContain('"code":-32600');
  });

  it('V3: M3-only, no headers, is -32602 at 400 (clientCapabilities missing)', async () => {
    const res = await handler()(
      post({
        jsonrpc: '2.0',
        id: 1,
        method: 'tools/call',
        params: {
          name: 'ping',
          _meta: { 'io.modelcontextprotocol/protocolVersion': '2026-07-28' },
        },
      }),
    );
    expect(res.status).toBe(400);
    expect(res.body).toContain('"code":-32602');
    expect(res.body).toContain('io.modelcontextprotocol/clientCapabilities');
  });

  it('V4: complete _meta but no MCP-Protocol-Version header is -32020 at 400', async () => {
    const res = await handler()(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'ping', _meta: MODERN_META } },
        { 'Mcp-Method': 'tools/call', 'Mcp-Name': 'ping' },
      ),
    );
    expect(res.status).toBe(400);
    expect(res.body).toContain('"code":-32020');
    expect(res.body).toContain('missing MCP-Protocol-Version header');
  });

  it('V6: a Mcp-Method header disagreeing with the body is -32020 at 400', async () => {
    const res = await handler()(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/list', params: { _meta: MODERN_META } },
        MODERN_HEADERS('tools/call'),
      ),
    );
    expect(res.status).toBe(400);
    expect(res.body).toContain('"code":-32020');
    expect(res.body).toContain('does not match body method');
  });

  it('V7: tools/call without a Mcp-Name header is -32020 at 400', async () => {
    const res = await handler()(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'ping', _meta: MODERN_META } },
        MODERN_HEADERS('tools/call'),
      ),
    );
    expect(res.status).toBe(400);
    expect(res.body).toContain('missing Mcp-Name header');
  });

  it('V7: an empty base64 sentinel decodes to "" and is rejected', async () => {
    const res = await handler()(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'ping', _meta: MODERN_META } },
        MODERN_HEADERS('tools/call', '=?base64??='),
      ),
    );
    expect(res.status).toBe(400);
    expect(res.body).toContain('decodes to an empty value');
  });

  it('V7: a base64-sentinel Mcp-Name matching the body is accepted', async () => {
    const encoded = `=?base64?${Buffer.from('ping').toString('base64')}?=`;
    const res = await handler()(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'ping', _meta: MODERN_META } },
        MODERN_HEADERS('tools/call', encoded),
      ),
    );
    expect(res.status).toBe(200);
    expect(res.body).toContain('"text":"pong\\n"');
  });

  it('V8: an unknown method with a valid modern envelope is -32601 at 404', async () => {
    const res = await handler()(
      post(
        { jsonrpc: '2.0', id: 1, method: 'nope', params: { _meta: MODERN_META } },
        MODERN_HEADERS('nope'),
      ),
    );
    expect(res.status).toBe(404);
    expect(res.body).toContain('"code":-32601');
  });

  it('duplicate headers: byte-identical values tolerated, differing rejected', async () => {
    const same = await handler()(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/list', params: { _meta: MODERN_META } },
        { 'MCP-Protocol-Version': '2026-07-28', 'Mcp-Method': ['tools/list', 'tools/list'] },
      ),
    );
    expect(same.status).toBe(200);

    const differing = await handler()(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/list', params: { _meta: MODERN_META } },
        { 'MCP-Protocol-Version': '2026-07-28', 'Mcp-Method': ['tools/list', 'tools/call'] },
      ),
    );
    expect(differing.status).toBe(400);
    expect(differing.body).toContain('conflicting duplicate values');
  });

  it('auth-required leaves are gated on the Authorization header', async () => {
    const blocked = await handler()(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'secret', _meta: MODERN_META } },
        MODERN_HEADERS('tools/call', 'secret'),
      ),
    );
    expect(blocked.status).toBe(401);
    expect(blocked.body).toContain('authentication required');
    // The refusal is still a complete modern result envelope.
    expect(blocked.body).toContain('"resultType":"complete"');

    const allowed = await handler()(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'secret', _meta: MODERN_META } },
        { ...MODERN_HEADERS('tools/call', 'secret'), Authorization: 'Bearer t' },
      ),
    );
    expect(allowed.status).toBe(200);
  });

  it('requires-confirmation leaves are gated on X-Confirm-Token (the default MRTR slot)', async () => {
    const blocked = await handler()(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'deploy', _meta: MODERN_META } },
        MODERN_HEADERS('tools/call', 'deploy'),
      ),
    );
    expect(blocked.status).toBe(428);
    expect(blocked.body).toContain('confirmation required');
    expect(blocked.body).toContain('"resultType":"complete"');

    const allowed = await handler()(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'deploy', _meta: MODERN_META } },
        { ...MODERN_HEADERS('tools/call', 'deploy'), 'X-Confirm-Token': 'ok' },
      ),
    );
    expect(allowed.status).toBe(200);
  });

  it('records the spec version and client identity in the audit bag', async () => {
    const seen: Array<Record<string, string> | undefined> = [];
    const bridge: McpBridge = {
      leaves: () => modernLockBridge().leaves(),
      invoke: (inv) => {
        seen.push(inv.meta.extra);
        return { stdout: 'pong\n', exitCode: 0 };
      },
    };
    await createMcpHandler(bridge)(
      post(
        {
          jsonrpc: '2.0',
          id: 1,
          method: 'tools/call',
          params: {
            name: 'ping',
            _meta: {
              ...MODERN_META,
              'io.modelcontextprotocol/clientInfo': { name: 'probe', version: '9.9' },
            },
          },
        },
        MODERN_HEADERS('tools/call', 'ping'),
      ),
    );
    expect(seen[0]).toEqual({
      mcp_spec_version: '2026-07-28',
      mcp_client_name: 'probe',
      mcp_client_version: '9.9',
    });
  });
});

describe('cacheable list results', () => {
  it('server/discover and tools/list carry ttlMs + cacheScope; tools/call does not', async () => {
    const handler = createMcpHandler(modernLockBridge(), {
      cacheHints: { ttlMs: 30_000, cacheScope: 'public' },
    });

    const discover = await handler(
      post(
        { jsonrpc: '2.0', id: 1, method: 'server/discover', params: { _meta: MODERN_META } },
        MODERN_HEADERS('server/discover'),
      ),
    );
    expect(discover.body).toContain('"ttlMs":30000');
    expect(discover.body).toContain('"cacheScope":"public"');

    const list = await handler(
      post(
        { jsonrpc: '2.0', id: 2, method: 'tools/list', params: { _meta: MODERN_META } },
        MODERN_HEADERS('tools/list'),
      ),
    );
    expect(list.body).toContain('"ttlMs":30000');
    expect(list.body).toContain('"cacheScope":"public"');

    const call = await handler(
      post(
        { jsonrpc: '2.0', id: 3, method: 'tools/call', params: { name: 'ping', _meta: MODERN_META } },
        MODERN_HEADERS('tools/call', 'ping'),
      ),
    );
    expect(call.body).not.toContain('ttlMs');
    expect(call.body).not.toContain('cacheScope');
  });

  it('defaults to ttlMs 0 / cacheScope private', () => {
    const cfg = resolveMcpConfig();
    expect(cfg.cacheTtlMs).toBe(0);
    expect(cfg.cacheScope).toBe('private');
  });
});

describe('tasks extension (unsupported)', () => {
  it('is not supported and is not advertised', async () => {
    expect(TASKS_SUPPORTED).toBe(false);
    const handler = createMcpHandler(modernLockBridge());
    const res = await handler(
      post(
        { jsonrpc: '2.0', id: 1, method: 'server/discover', params: { _meta: MODERN_META } },
        MODERN_HEADERS('server/discover'),
      ),
    );
    // capabilities.extensions is omitted entirely, not emitted empty.
    expect(res.body).not.toContain('extensions');
    expect(res.body).toContain('"capabilities":{"tools":{}}');
  });

  it('answers every tasks/* method with -32601 at 404, like any unknown method', async () => {
    const handler = createMcpHandler(modernLockBridge());
    for (const method of TASK_METHODS) {
      expect(isTaskMethod(method)).toBe(true);
      const res = await handler(
        post(
          { jsonrpc: '2.0', id: 1, method, params: { _meta: MODERN_META } },
          MODERN_HEADERS(method),
        ),
      );
      expect(res.status, method).toBe(404);
      expect(res.body, method).toBe(
        `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found: ${method}"}}\n`,
      );
    }
  });
});

describe('mount options', () => {
  it('refuses an explicitly empty spec-version set', () => {
    expect(() => resolveMcpConfig({ specVersions: [] })).toThrow(
      /at least one spec version/,
    );
  });

  it('refuses an unrecognized spec version', () => {
    expect(() =>
      resolveMcpConfig({ specVersions: ['1999-01-01' as never] }),
    ).toThrow(/unrecognized version/);
  });

  it('refuses a negative ttl and an unknown cache scope', () => {
    expect(() => resolveMcpConfig({ cacheHints: { ttlMs: -1 } })).toThrow(
      /negative ttl/,
    );
    expect(() =>
      resolveMcpConfig({ cacheHints: { cacheScope: 'shared' as never } }),
    ).toThrow(/unknown cache scope/);
  });

  it('refuses an empty confirmation key', () => {
    expect(() => resolveMcpConfig({ confirmationKey: '' })).toThrow(
      /empty key/,
    );
  });

  it('defaults to both eras, /mcp, and an empty origin allowlist', () => {
    const cfg = resolveMcpConfig();
    expect(cfg.legacyEnabled).toBe(true);
    expect(cfg.modernEnabled).toBe(true);
    expect(cfg.path).toBe('/mcp');
    expect(cfg.originAllowlist).toEqual([]);
  });

  it('legacy-only mounts ignore modern markers entirely', async () => {
    const handler = createMcpHandler(legacyLockBridge(), {
      specVersions: ['2024-11-05'],
    });
    const res = await handler(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/list', params: { _meta: MODERN_META } },
        MODERN_HEADERS('tools/list'),
      ),
    );
    // Served by the legacy handler: no resultType, no cache hints.
    expect(res.status).toBe(200);
    expect(res.body).not.toContain('resultType');
  });

  it('modern-only mounts reject a bare initialize rather than demoting it', async () => {
    const handler = createMcpHandler(modernLockBridge(), {
      specVersions: ['2026-07-28'],
    });
    const res = await handler(post({ jsonrpc: '2.0', id: 1, method: 'initialize' }));
    expect(res.status).toBe(400);
    // A modern-only server names its supported versions in any error
    // it returns to initialize: legacy clients have no fall-forward.
    expect(res.body).toContain('supported protocol versions: 2026-07-28');
  });

  it('enforces the Origin allowlist only when configured', async () => {
    const open = createMcpHandler(modernLockBridge());
    const openRes = await open(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/list', params: { _meta: MODERN_META } },
        { ...MODERN_HEADERS('tools/list'), Origin: 'https://evil.example' },
      ),
    );
    expect(openRes.status).toBe(200);

    const gated = createMcpHandler(modernLockBridge(), {
      originAllowlist: ['https://good.example'],
    });
    const blocked = await gated(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/list', params: { _meta: MODERN_META } },
        { ...MODERN_HEADERS('tools/list'), Origin: 'https://evil.example' },
      ),
    );
    expect(blocked.status).toBe(403);

    const allowed = await gated(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/list', params: { _meta: MODERN_META } },
        { ...MODERN_HEADERS('tools/list'), Origin: 'https://good.example' },
      ),
    );
    expect(allowed.status).toBe(200);
  });

  it('answers GET and DELETE with 405 when the modern era is enabled', async () => {
    const handler = createMcpHandler(modernLockBridge());
    for (const method of ['GET', 'DELETE']) {
      const res = await handler({ method, headers: {}, body: '' });
      expect(res.status, method).toBe(405);
    }
  });

  it('honours a custom serverInfo on both eras', async () => {
    const opts = { serverInfo: { name: 'probe', version: '1.2.3' } };
    const legacy = await createMcpHandler(legacyLockBridge(), opts)(
      post({ jsonrpc: '2.0', id: 1, method: 'initialize' }),
    );
    expect(legacy.body).toContain('"name":"probe","version":"1.2.3"');

    const modern = await createMcpHandler(modernLockBridge(), opts)(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/list', params: { _meta: MODERN_META } },
        MODERN_HEADERS('tools/list'),
      ),
    );
    expect(modern.body).toContain('"name":"probe","version":"1.2.3"');
  });
});

describe('safety gate (Policy.Allowed)', () => {
  const destructive = { destructive: true };

  it('always allows the local-runtime surfaces', () => {
    const p = defaultPolicy();
    expect(policyAllowed(p, destructive, SURFACE_CLI)).toBe(true);
    expect(policyAllowed(p, destructive, SURFACE_LIB)).toBe(true);
  });

  it('allows non-destructive commands on every surface', () => {
    expect(policyAllowed(defaultPolicy(), {}, SURFACE_MCP)).toBe(true);
  });

  it('blocks destructive commands on remote surfaces by default', () => {
    expect(policyAllowed(defaultPolicy(), destructive, SURFACE_MCP)).toBe(false);
  });

  it('allows destructive commands only on explicitly named surfaces', () => {
    const p = { allowDestructiveOn: [SURFACE_MCP] };
    expect(policyAllowed(p, destructive, SURFACE_MCP)).toBe(true);
    expect(policyAllowed(p, destructive, 'rest')).toBe(false);
  });

  it('opting MCP in makes a destructive leaf reachable on both eras', async () => {
    const opts = { policy: { allowDestructiveOn: [SURFACE_MCP] } };
    // The lock bridges apply the DEFAULT policy internally, so this
    // asserts the option plumbing on a bridge that honours it.
    const bridge: McpBridge = {
      leaves: () => modernLockBridge().leaves(),
      invoke: () => ({ stdout: 'deleted\n', exitCode: 0 }),
    };
    const res = await createMcpHandler(bridge, opts)(
      post(
        { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'widget.delete', _meta: MODERN_META } },
        MODERN_HEADERS('tools/call', 'widget.delete'),
      ),
    );
    expect(res.status).toBe(200);
    expect(res.body).toContain('"isError":false');
  });
});

describe('wire encoding', () => {
  it('sorts object keys lexicographically, as Go does for maps', () => {
    expect(canonicalJSONStringify({ b: 1, a: 2, c: 3 })).toBe(
      '{"a":2,"b":1,"c":3}',
    );
  });

  it('emits envelope members in Go struct order with a trailing newline', () => {
    expect(
      encodeEnvelope({ id: new RawJSON('1'), result: { z: 1, a: 2 } }),
    ).toBe('{"jsonrpc":"2.0","id":1,"result":{"a":2,"z":1}}\n');
  });

  it('round-trips an explicit null id and omits an absent one', () => {
    expect(encodeEnvelope({ id: new RawJSON('null'), result: {} })).toBe(
      '{"jsonrpc":"2.0","id":null,"result":{}}\n',
    );
    expect(encodeEnvelope({ result: {} })).toBe(
      '{"jsonrpc":"2.0","result":{}}\n',
    );
  });

  it('places error data after message', () => {
    expect(
      encodeEnvelope({
        id: new RawJSON('5'),
        error: { code: -32022, message: 'boom', data: { b: 1, a: 2 } },
      }),
    ).toBe(
      '{"jsonrpc":"2.0","id":5,"error":{"code":-32022,"message":"boom","data":{"a":2,"b":1}}}\n',
    );
  });
});

describe('helpers', () => {
  it('validModernRequestId accepts strings and integers only', () => {
    expect(validModernRequestId(new RawJSON('"a"'))).toBe(true);
    expect(validModernRequestId(new RawJSON('7'))).toBe(true);
    expect(validModernRequestId(new RawJSON('null'))).toBe(false);
    expect(validModernRequestId(new RawJSON('true'))).toBe(false);
    expect(validModernRequestId(new RawJSON('1.5'))).toBe(false);
    expect(validModernRequestId(new RawJSON('{}'))).toBe(false);
    expect(validModernRequestId(new RawJSON('[]'))).toBe(false);
  });

  it('decodeMcpSentinel passes plain values and fails closed on bad base64', () => {
    expect(decodeMcpSentinel('ping')).toBe('ping');
    expect(decodeMcpSentinel(`=?base64?${Buffer.from('a.b').toString('base64')}?=`)).toBe('a.b');
    expect(decodeMcpSentinel('=?base64?!!!?=')).toBeUndefined();
  });

  it('rawToolCallName distinguishes absent from non-string', () => {
    expect(rawToolCallName({ name: 'x' })).toEqual({
      name: 'x',
      present: true,
      isString: true,
    });
    expect(rawToolCallName({})).toEqual({
      name: '',
      present: false,
      isString: false,
    });
    expect(rawToolCallName({ name: 12 })).toEqual({
      name: '',
      present: true,
      isString: false,
    });
  });

  it('parseModernMeta requires both reserved keys', () => {
    const ok = parseModernMeta({ _meta: MODERN_META });
    expect('meta' in ok && ok.meta.version).toBe('2026-07-28');

    const noCaps = parseModernMeta({
      _meta: { 'io.modelcontextprotocol/protocolVersion': '2026-07-28' },
    });
    expect('error' in noCaps).toBe(true);

    const noParams = parseModernMeta(undefined);
    expect('error' in noParams).toBe(true);
  });
});
