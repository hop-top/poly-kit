/**
 * The mount-level audit hook: one record per refusal the surface
 * decides before adopter code runs, mirroring what Go's bridge sinks
 * receive from the MCP surface. Pins, per event and per era, the
 * record's fields, that the wire response is byte-identical with and
 * without the hook, that a throwing hook changes nothing, and that
 * neither a successful call nor an expired-but-authentic state is
 * recorded.
 */

import { PassThrough } from 'node:stream';
import { describe, expect, it } from 'vitest';

import {
  MCP_AUDIT_AUTH_REFUSED,
  MCP_AUDIT_CONFIRMATION_DECLINED,
  MCP_AUDIT_CONFIRMATION_REQUIRED,
  MCP_AUDIT_CONFIRMATION_STATE_REJECTED,
  MCP_AUDIT_DESTRUCTIVE_BLOCKED,
  MCP_CONFIRM_REJECTION_STATE,
} from './audit.js';
import {
  createMcpHandler,
  mcpMountConfig,
  type McpHttpRequest,
  type McpHttpResponse,
} from './dispatch.js';
import {
  ElicitationConfirmGate,
  mcpConfirmArgsDigest,
  mintMcpConfirmState,
} from './modern-confirm.js';
import { serveStdio } from './stdio.js';
import { legacyLockBridge, modernLockBridge, mrtrLockBridge } from './testtree.js';
import {
  MCP_SPEC_2024_11_05,
  MCP_SPEC_2026_07_28,
  type McpAuditRecord,
  type McpBridge,
  type McpMountOptions,
} from './types.js';

const KEY = 'audit-unit-suite-shared-secret-32';

/** Collects every record the hook receives. */
function recorder(): {
  records: McpAuditRecord[];
  audit: (r: McpAuditRecord) => void;
} {
  const records: McpAuditRecord[] = [];
  return { records, audit: (r) => records.push(r) };
}

/** A legacy-era tools/call. */
function legacyCall(
  name: string,
  headers: Record<string, string> = {},
): McpHttpRequest {
  return {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...headers },
    body: JSON.stringify({
      jsonrpc: '2.0',
      id: 1,
      method: 'tools/call',
      params: { name },
    }),
  };
}

/** The reserved _meta bag, with the client's elicitation declaration. */
function meta(elicitation?: unknown): Record<string, unknown> {
  const caps: Record<string, unknown> = {};
  if (elicitation !== undefined) caps.elicitation = elicitation;
  return {
    'io.modelcontextprotocol/clientCapabilities': caps,
    'io.modelcontextprotocol/protocolVersion': MCP_SPEC_2026_07_28,
  };
}

interface ModernCallOpts {
  id?: number;
  tool: string;
  args?: Record<string, unknown>;
  meta?: Record<string, unknown>;
  state?: string;
  action?: string;
  headers?: Record<string, string>;
}

/** A modern-era tools/call, with optional MRTR retry members. */
function modernCall(o: ModernCallOpts): McpHttpRequest {
  const params: Record<string, unknown> = {
    name: o.tool,
    _meta: o.meta ?? meta({}),
  };
  if (o.args !== undefined) params.arguments = o.args;
  if (o.state !== undefined) params.requestState = o.state;
  if (o.action !== undefined) {
    params.inputResponses = { confirm: { action: o.action } };
  }
  return {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'MCP-Protocol-Version': MCP_SPEC_2026_07_28,
      'Mcp-Method': 'tools/call',
      'Mcp-Name': o.tool,
      ...(o.headers ?? {}),
    },
    body: JSON.stringify({
      jsonrpc: '2.0',
      id: o.id ?? 1,
      method: 'tools/call',
      params,
    }),
  };
}

/**
 * Serves `reqs` in order against two fresh mounts, one silent and one
 * audited, and asserts every response is byte-identical between them.
 * Returns the audited mount's last response and its records.
 */
async function sameWire(
  make: () => McpBridge,
  opts: McpMountOptions,
  reqs: McpHttpRequest[],
): Promise<{ res: McpHttpResponse; records: McpAuditRecord[] }> {
  const silent = createMcpHandler(make(), opts);
  const { records, audit } = recorder();
  const audited = createMcpHandler(make(), { ...opts, audit });
  let last: McpHttpResponse | undefined;
  for (const req of reqs) {
    const a = await silent(req);
    const b = await audited(req);
    expect(b).toEqual(a);
    last = b;
  }
  if (last === undefined) throw new Error('no request served');
  return { res: last, records };
}

/** The one record expected for a refusal, checked field by field. */
function expectRecord(
  r: McpAuditRecord | undefined,
  want: { path: string[]; specVersion: string; error: string; extra?: Record<string, string> },
): void {
  expect(r).toBeDefined();
  if (r === undefined) return;
  expect(r.path).toEqual(want.path);
  expect(r.surface).toBe('mcp');
  expect(r.requestedAt).toBeInstanceOf(Date);
  expect(r.specVersion).toBe(want.specVersion);
  expect(r.error).toBe(want.error);
  expect(r.extra).toEqual({
    mcp_spec_version: want.specVersion,
    ...(want.extra ?? {}),
  });
}

describe('audit hook — 2024-11-05 era', () => {
  it('auth refused: one record, wire unchanged', async () => {
    const { res, records } = await sameWire(legacyLockBridge, {}, [
      legacyCall('secret', { Authorization: 'Bearer presence-only' }),
    ]);
    expect(res.status).toBe(401);
    expect(records).toHaveLength(1);
    expectRecord(records[0], {
      path: ['secret'],
      specVersion: MCP_SPEC_2024_11_05,
      error: MCP_AUDIT_AUTH_REFUSED,
    });
  });

  it('confirmation required: one record, wire unchanged', async () => {
    const { res, records } = await sameWire(legacyLockBridge, {}, [
      legacyCall('deploy'),
    ]);
    expect(res.status).toBe(428);
    expect(records).toHaveLength(1);
    expectRecord(records[0], {
      path: ['deploy'],
      specVersion: MCP_SPEC_2024_11_05,
      error: MCP_AUDIT_CONFIRMATION_REQUIRED,
    });
  });

  it('destructive ceiling: one record, wire unchanged', async () => {
    const { res, records } = await sameWire(legacyLockBridge, {}, [
      legacyCall('widget.delete'),
    ]);
    expect(res.status).toBe(200);
    expect(res.body).toContain('destructive command blocked');
    expect(records).toHaveLength(1);
    expectRecord(records[0], {
      path: ['widget', 'delete'],
      specVersion: MCP_SPEC_2024_11_05,
      error: MCP_AUDIT_DESTRUCTIVE_BLOCKED,
    });
  });

  it('a successful call and a satisfied gate record nothing', async () => {
    const { records } = await sameWire(legacyLockBridge, {}, [
      legacyCall('ping'),
      legacyCall('deploy', { 'X-Confirm-Token': 'yes' }),
    ]);
    expect(records).toEqual([]);
  });

  it('a throwing hook leaves the response unchanged', async () => {
    const silent = await createMcpHandler(legacyLockBridge())(legacyCall('secret'));
    const loud = await createMcpHandler(legacyLockBridge(), {
      audit: () => {
        throw new Error('sink down');
      },
    })(legacyCall('secret'));
    expect(loud).toEqual(silent);
  });
});

describe('audit hook — 2026-07-28 era', () => {
  it('auth refused: one record, wire unchanged', async () => {
    const { res, records } = await sameWire(modernLockBridge, {}, [
      modernCall({ tool: 'secret', headers: { Authorization: 'Bearer presence-only' } }),
    ]);
    expect(res.status).toBe(401);
    expect(records).toHaveLength(1);
    expectRecord(records[0], {
      path: ['secret'],
      specVersion: MCP_SPEC_2026_07_28,
      error: MCP_AUDIT_AUTH_REFUSED,
    });
  });

  it('confirmation required (header gate, key-less mount): one record', async () => {
    const { res, records } = await sameWire(modernLockBridge, {}, [
      modernCall({ tool: 'deploy' }),
    ]);
    expect(res.status).toBe(428);
    expect(records).toHaveLength(1);
    expectRecord(records[0], {
      path: ['deploy'],
      specVersion: MCP_SPEC_2026_07_28,
      error: MCP_AUDIT_CONFIRMATION_REQUIRED,
    });
  });

  it('confirmation required (keyed mount, client without elicitation): one record', async () => {
    const { res, records } = await sameWire(
      () => mrtrLockBridge().bridge,
      { confirmationKey: KEY },
      [modernCall({ tool: 'purge', meta: meta() })],
    );
    expect(res.status).toBe(428);
    expect(records).toHaveLength(1);
    expectRecord(records[0], {
      path: ['purge'],
      specVersion: MCP_SPEC_2026_07_28,
      error: MCP_AUDIT_CONFIRMATION_REQUIRED,
    });
  });

  for (const action of ['decline', 'cancel']) {
    it(`confirmation declined (${action}): one record, wire unchanged`, async () => {
      const silent = createMcpHandler(mrtrLockBridge().bridge, { confirmationKey: KEY });
      const { records, audit } = recorder();
      const audited = createMcpHandler(mrtrLockBridge().bridge, {
        confirmationKey: KEY,
        audit,
      });
      // The prompt itself is not a refusal: nothing is recorded.
      const r1 = await audited(modernCall({ id: 1, tool: 'purge' }));
      const state = (JSON.parse(r1.body) as { result: { requestState: string } }).result
        .requestState;
      expect(records).toEqual([]);

      const retry = modernCall({ id: 2, tool: 'purge', state, action });
      const a = await silent(retry);
      const b = await audited(retry);
      expect(b).toEqual(a);
      expect(b.body).toContain('confirmation declined');
      expect(records).toHaveLength(1);
      expectRecord(records[0], {
        path: ['purge'],
        specVersion: MCP_SPEC_2026_07_28,
        error: MCP_AUDIT_CONFIRMATION_DECLINED,
      });
    });
  }

  it('state rejected: one record carrying mcp_confirm_rejection, wire shape unchanged', async () => {
    const { records, audit } = recorder();
    const handler = createMcpHandler(mrtrLockBridge().bridge, {
      confirmationKey: KEY,
      audit,
    });
    const r1 = await handler(modernCall({ id: 1, tool: 'purge' }));
    const state = (JSON.parse(r1.body) as { result: { requestState: string } }).result
      .requestState;
    const forged = state.slice(0, -1) + (state.endsWith('A') ? 'B' : 'A');

    const r2 = await handler(modernCall({ id: 2, tool: 'purge', state: forged, action: 'accept' }));
    const result = (JSON.parse(r2.body) as { result: Record<string, unknown> }).result;
    // Re-prompted with fresh state, exactly as without the hook.
    expect(result.resultType).toBe('input_required');
    expect(result.requestState).not.toBe(forged);
    expect(records).toHaveLength(1);
    expectRecord(records[0], {
      path: ['purge'],
      specVersion: MCP_SPEC_2026_07_28,
      error: MCP_AUDIT_CONFIRMATION_STATE_REJECTED,
      extra: { mcp_confirm_rejection: MCP_CONFIRM_REJECTION_STATE },
    });
  });

  it('an authentic but expired state re-prompts without a record', async () => {
    const { records, audit } = recorder();
    const handler = createMcpHandler(mrtrLockBridge().bridge, {
      confirmationKey: KEY,
      audit,
    });
    // Minted under the mount's key for this exact binding: tool
    // "purge", no arguments, no Authorization header. An hour stale.
    const stale = mintMcpConfirmState(
      new TextEncoder().encode(KEY),
      { tool: 'purge', argsDigest: mcpConfirmArgsDigest({}), principal: '' },
      Math.floor(Date.now() / 1000) - 3600,
    );
    const res = await handler(modernCall({ tool: 'purge', state: stale, action: 'accept' }));
    const result = (JSON.parse(res.body) as { result: Record<string, unknown> }).result;
    expect(result.resultType).toBe('input_required');
    expect(records).toEqual([]);
  });

  it('destructive ceiling: one record, wire unchanged', async () => {
    const { res, records } = await sameWire(modernLockBridge, {}, [
      modernCall({ tool: 'widget.delete' }),
    ]);
    expect(res.status).toBe(200);
    expect(res.body).toContain('destructive command blocked');
    expect(records).toHaveLength(1);
    expectRecord(records[0], {
      path: ['widget', 'delete'],
      specVersion: MCP_SPEC_2026_07_28,
      error: MCP_AUDIT_DESTRUCTIVE_BLOCKED,
    });
  });

  it('destructive ceiling behind an accepted confirmation is still recorded', async () => {
    const { records, audit } = recorder();
    const handler = createMcpHandler(mrtrLockBridge().bridge, {
      confirmationKey: KEY,
      audit,
    });
    const r1 = await handler(modernCall({ id: 1, tool: 'vault.burn' }));
    const state = (JSON.parse(r1.body) as { result: { requestState: string } }).result
      .requestState;
    const r2 = await handler(modernCall({ id: 2, tool: 'vault.burn', state, action: 'accept' }));
    expect(r2.body).toContain('destructive command blocked');
    expect(records).toHaveLength(1);
    expectRecord(records[0], {
      path: ['vault', 'burn'],
      specVersion: MCP_SPEC_2026_07_28,
      error: MCP_AUDIT_DESTRUCTIVE_BLOCKED,
    });
  });

  it('a successful call, a satisfied gate and an accepted prompt record nothing', async () => {
    const { records, audit } = recorder();
    const handler = createMcpHandler(mrtrLockBridge().bridge, {
      confirmationKey: KEY,
      audit,
    });
    const r1 = await handler(modernCall({ id: 1, tool: 'purge' }));
    const state = (JSON.parse(r1.body) as { result: { requestState: string } }).result
      .requestState;
    const r2 = await handler(modernCall({ id: 2, tool: 'purge', state, action: 'accept' }));
    expect(r2.body).toContain('"isError":false');
    const r3 = await handler(
      modernCall({ id: 3, tool: 'purge', meta: meta(), headers: { 'X-Confirm-Token': 'yes' } }),
    );
    expect(r3.body).toContain('"isError":false');
    expect(records).toEqual([]);

    const { records: plain, audit: auditPlain } = recorder();
    await createMcpHandler(modernLockBridge(), { audit: auditPlain })(modernCall({ tool: 'ping' }));
    expect(plain).toEqual([]);
  });

  it('a throwing hook leaves the response unchanged', async () => {
    const req = modernCall({ tool: 'secret' });
    const silent = await createMcpHandler(modernLockBridge())(req);
    const loud = await createMcpHandler(modernLockBridge(), {
      audit: () => {
        throw new Error('sink down');
      },
    })(req);
    expect(loud).toEqual(silent);
  });
});

describe('audit hook — ElicitationConfirmGate keeps its in-memory rejections', () => {
  const leaf = {
    path: ['purge'],
    description: 'Purge a target',
    properties: {},
    class: { requiresConfirmation: true },
  };

  it('a rejected state reaches both the rejections list and the hook', () => {
    const { records, audit } = recorder();
    const gate = new ElicitationConfirmGate(new TextEncoder().encode(KEY), undefined, audit);
    const decision = gate.evaluate(
      { method: 'POST', headers: {}, body: '' },
      leaf,
      { name: 'purge', _meta: meta({}), requestState: 'v1.99999999999.AAAA' },
    );
    expect(decision?.result.resultType).toBe('input_required');
    expect(gate.rejections).toEqual([
      { path: 'purge', mcpConfirmRejection: MCP_CONFIRM_REJECTION_STATE },
    ]);
    expect(records).toHaveLength(1);
    expectRecord(records[0], {
      path: ['purge'],
      specVersion: MCP_SPEC_2026_07_28,
      error: MCP_AUDIT_CONFIRMATION_STATE_REJECTED,
      extra: { mcp_confirm_rejection: MCP_CONFIRM_REJECTION_STATE },
    });
  });

  it('a throwing hook still records the rejection in memory', () => {
    const gate = new ElicitationConfirmGate(new TextEncoder().encode(KEY), undefined, () => {
      throw new Error('sink down');
    });
    const decision = gate.evaluate(
      { method: 'POST', headers: {}, body: '' },
      leaf,
      { name: 'purge', _meta: meta({}), requestState: 'v1.99999999999.AAAA' },
    );
    expect(decision?.result.resultType).toBe('input_required');
    expect(gate.rejections).toHaveLength(1);
  });
});

describe('audit hook — plumbing', () => {
  it('the resolved mount config carries the hook', () => {
    const { audit } = recorder();
    expect(mcpMountConfig({ audit }).audit).toBe(audit);
    expect(mcpMountConfig({}).audit).toBeUndefined();
  });

  it('serveStdio records a rejected state for a bridge with mount options', async () => {
    const { records, audit } = recorder();
    const input = new PassThrough();
    const output = new PassThrough();
    let out = '';
    output.on('data', (c: Buffer) => (out += c.toString('utf8')));
    const lines = () => out.split('\n').filter((l) => l !== '');
    const server = serveStdio(mrtrLockBridge().bridge, {
      input,
      output,
      log: new PassThrough(),
      mount: { confirmationKey: KEY, audit },
    });
    const call = (id: number, extra: Record<string, unknown>) =>
      JSON.stringify({
        jsonrpc: '2.0',
        id,
        method: 'tools/call',
        params: { name: 'purge', _meta: meta({}), ...extra },
      });
    const reply = async (n: number) => {
      while (lines().length < n) await new Promise((r) => setTimeout(r, 1));
      return JSON.parse(lines()[n - 1] ?? '{}') as { result: Record<string, unknown> };
    };

    input.write(`${call(1, {})}\n`);
    const r1 = await reply(1);
    const state = String(r1.result.requestState);
    const forged = state.slice(0, -1) + (state.endsWith('A') ? 'B' : 'A');
    expect(records).toEqual([]);

    input.write(
      `${call(2, { requestState: forged, inputResponses: { confirm: { action: 'accept' } } })}\n`,
    );
    const r2 = await reply(2);
    expect(r2.result.resultType).toBe('input_required');
    input.end();
    await server.done;

    expect(records).toHaveLength(1);
    expectRecord(records[0], {
      path: ['purge'],
      specVersion: MCP_SPEC_2026_07_28,
      error: MCP_AUDIT_CONFIRMATION_STATE_REJECTED,
      extra: { mcp_confirm_rejection: MCP_CONFIRM_REJECTION_STATE },
    });
  });

  it('serveStdio reuses a createMcpHandler handler hook included', async () => {
    const { records, audit } = recorder();
    const handler = createMcpHandler(legacyLockBridge(), { audit });
    const input = new PassThrough();
    const output = new PassThrough();
    let out = '';
    output.on('data', (c: Buffer) => (out += c.toString('utf8')));
    const server = serveStdio(handler, { input, output, log: new PassThrough() });
    input.write('{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"secret"}}\n');
    input.end();
    await server.done;
    expect(out).toContain('authentication required');
    expect(records).toHaveLength(1);
    expectRecord(records[0], {
      path: ['secret'],
      specVersion: MCP_SPEC_2024_11_05,
      error: MCP_AUDIT_AUTH_REFUSED,
    });
  });
});
