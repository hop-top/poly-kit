/**
 * MRTR confirmation loop: the edge cases the wire fixture cannot
 * express.
 *
 * The fixture pins one happy exchange. Everything else that makes the
 * gate safe rather than merely functional — refusal on decline, the
 * two distinct verification failures, the three binding components,
 * and above all the two paths that must keep coexisting (header
 * fallback for capability-less clients, header gate for key-less
 * mounts) — is pinned here, mirroring the Go suite's coverage.
 */

import { describe, expect, it } from 'vitest';

import { createMcpHandler } from './dispatch.js';
import {
  ElicitationConfirmGate,
  mcpConfirmArgsDigest,
  mintMcpConfirmState,
  MCP_CONFIRM_STATE_TTL_SECONDS,
  verifyMcpConfirmState,
  type McpConfirmBinding,
} from './modern-confirm.js';
import { modernLockBridge, mrtrLockBridge } from './testtree.js';
import { MCP_SPEC_2026_07_28 } from './types.js';

const KEY = 'mrtr-unit-suite-shared-secret-32b';

/** The reserved _meta bag, with the client's elicitation declaration. */
function meta(elicitation?: unknown): Record<string, unknown> {
  const caps: Record<string, unknown> = {};
  if (elicitation !== undefined) caps.elicitation = elicitation;
  return {
    'io.modelcontextprotocol/clientCapabilities': caps,
    'io.modelcontextprotocol/protocolVersion': MCP_SPEC_2026_07_28,
  };
}

interface CallOpts {
  id?: number;
  tool?: string;
  args?: Record<string, unknown>;
  meta?: Record<string, unknown>;
  state?: string;
  action?: string;
  headers?: Record<string, string>;
}

/** Builds one modern tools/call request, with optional MRTR retry members. */
function call(o: CallOpts): { headers: Record<string, string>; body: string } {
  const tool = o.tool ?? 'purge';
  const params: Record<string, unknown> = {
    name: tool,
    _meta: o.meta ?? meta({}),
  };
  if (o.args !== undefined) params.arguments = o.args;
  if (o.state !== undefined) params.requestState = o.state;
  if (o.action !== undefined) {
    params.inputResponses = { confirm: { action: o.action } };
  }
  return {
    headers: {
      'Content-Type': 'application/json',
      'MCP-Protocol-Version': MCP_SPEC_2026_07_28,
      'Mcp-Method': 'tools/call',
      'Mcp-Name': tool,
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

/** A keyed MRTR mount plus its execution counter and gate. */
function mount(opts: { key?: string | undefined } = {}) {
  const { bridge, executions } = mrtrLockBridge();
  const handler = createMcpHandler(bridge, {
    confirmationKey: 'key' in opts ? opts.key : KEY,
  });
  return { handler, executions };
}

/** Posts one request and decodes the JSON-RPC result. */
async function post(
  handler: ReturnType<typeof createMcpHandler>,
  o: CallOpts,
): Promise<{ status: number; result: Record<string, unknown> }> {
  const { headers, body } = call(o);
  const res = await handler({ method: 'POST', headers, body });
  const decoded = JSON.parse(res.body) as {
    result?: Record<string, unknown>;
  };
  return { status: res.status, result: decoded.result ?? {} };
}

/** Asserts a result is an input_required prompt and returns its state. */
function promptState(result: Record<string, unknown>): string {
  expect(result.resultType).toBe('input_required');
  expect(typeof result.requestState).toBe('string');
  return result.requestState as string;
}

describe('MRTR confirmation — full loop', () => {
  it('round 1 prompts without executing, round 2 accepts and executes', async () => {
    const { handler, executions } = mount();
    const args = { target: 'data' };

    const r1 = await post(handler, { id: 1, args });
    expect(r1.status).toBe(200);
    const state = promptState(r1.result);
    expect(executions()).toBe(0);

    // The prompt is one elicitation/create form request under the
    // reserved key, and carries no execution members.
    const requests = r1.result.inputRequests as Record<string, unknown>;
    expect(Object.keys(requests)).toEqual(['confirm']);
    const confirm = requests.confirm as Record<string, unknown>;
    expect(confirm.method).toBe('elicitation/create');
    const cp = confirm.params as Record<string, unknown>;
    expect(cp.mode).toBe('form');
    expect(String(cp.message)).toContain('purge');
    expect(cp.requestedSchema).toEqual({ type: 'object', properties: {} });
    expect('content' in r1.result).toBe(false);
    expect('isError' in r1.result).toBe(false);

    // The modern envelope is still stamped on the interim result.
    const m = r1.result._meta as Record<string, unknown>;
    expect(m['io.modelcontextprotocol/serverInfo']).toBeDefined();

    const r2 = await post(handler, { id: 2, args, state, action: 'accept' });
    expect(r2.status).toBe(200);
    expect(r2.result.resultType).toBe('complete');
    expect(r2.result.isError).toBe(false);
    expect(executions()).toBe(1);
  });

  it('interim input_required results are never cacheable', async () => {
    const { handler } = mount();
    const { result } = await post(handler, {});
    expect('ttlMs' in result).toBe(false);
    expect('cacheScope' in result).toBe(false);
  });
});

describe('MRTR confirmation — refusals and re-prompts', () => {
  for (const action of ['decline', 'cancel']) {
    it(`${action} refuses the call without executing`, async () => {
      const { handler, executions } = mount();
      const r1 = await post(handler, { id: 1 });
      const state = promptState(r1.result);

      const r2 = await post(handler, { id: 2, state, action });
      expect(r2.status).toBe(200);
      expect(r2.result.resultType).toBe('complete');
      expect(r2.result.isError).toBe(true);
      expect(JSON.stringify(r2.result.content)).toContain(
        'confirmation declined',
      );
      expect(executions()).toBe(0);
    });
  }

  it('a valid state with a missing answer re-prompts rather than erroring', async () => {
    const { handler, executions } = mount();
    const r1 = await post(handler, { id: 1 });
    const state = promptState(r1.result);

    // Echoed state, no inputResponses at all.
    const r2 = await post(handler, { id: 2, state });
    expect(r2.status).toBe(200);
    promptState(r2.result);
    expect(executions()).toBe(0);
  });

  it('an unknown action re-prompts rather than accepting', async () => {
    const { handler, executions } = mount();
    const r1 = await post(handler, { id: 1 });
    const state = promptState(r1.result);

    const r2 = await post(handler, { id: 2, state, action: 'maybe' });
    promptState(r2.result);
    expect(executions()).toBe(0);
  });

  it('a tampered state is audited, never honored, and re-prompted', async () => {
    const { bridge, executions } = mrtrLockBridge();
    const handler = createMcpHandler(bridge, { confirmationKey: KEY });
    const r1 = await post(handler, { id: 1 });
    const state = promptState(r1.result);

    // Flip the last MAC character: authentic framing, forged tag.
    const forged =
      state.slice(0, -1) + (state.endsWith('A') ? 'B' : 'A');
    const r2 = await post(handler, {
      id: 2,
      state: forged,
      action: 'accept',
    });
    // Re-prompted with FRESH state, not executed.
    const reissued = promptState(r2.result);
    expect(reissued).not.toBe(forged);
    expect(executions()).toBe(0);
  });

  it('records the rejection as a security-relevant audit event', () => {
    const gate = new ElicitationConfirmGate(new TextEncoder().encode(KEY));
    const leaf = {
      path: ['purge'],
      description: 'Purge a target',
      properties: {},
      class: { requiresConfirmation: true },
    };
    const decision = gate.evaluate(
      { method: 'POST', headers: {}, body: '' },
      leaf,
      { name: 'purge', _meta: meta({}), requestState: 'v1.99999999999.AAAA' },
    );
    expect(decision?.result.resultType).toBe('input_required');
    expect(gate.rejections).toEqual([
      {
        path: 'purge',
        mcpConfirmRejection: 'request_state_verification_failed',
      },
    ]);
  });

  it('an authentic but expired state re-prompts WITHOUT an audit event', () => {
    const gate = new ElicitationConfirmGate(new TextEncoder().encode(KEY));
    const leaf = {
      path: ['purge'],
      description: 'Purge a target',
      properties: {},
      class: { requiresConfirmation: true },
    };
    const params = { name: 'purge', _meta: meta({}), arguments: {} };
    const binding: McpConfirmBinding = {
      tool: 'purge',
      argsDigest: mcpConfirmArgsDigest(params),
      principal: '',
    };
    // Authentic, minted under this key, but an hour stale.
    const stale = mintMcpConfirmState(
      new TextEncoder().encode(KEY),
      binding,
      Math.floor(Date.now() / 1000) - 3600,
    );
    const decision = gate.evaluate(
      { method: 'POST', headers: {}, body: '' },
      leaf,
      { ...params, requestState: stale, inputResponses: { confirm: { action: 'accept' } } },
    );
    expect(decision?.result.resultType).toBe('input_required');
    // Expiry is routine, not suspicious: no audit event.
    expect(gate.rejections).toEqual([]);
  });
});

describe('MRTR confirmation — state binding', () => {
  it('state minted for one argument set cannot authorize another', async () => {
    const { handler, executions } = mount();
    const r1 = await post(handler, { id: 1, args: { target: 'data' } });
    const state = promptState(r1.result);

    // Same tool, same principal, DIFFERENT arguments.
    const r2 = await post(handler, {
      id: 2,
      args: { target: 'everything' },
      state,
      action: 'accept',
    });
    promptState(r2.result);
    expect(executions()).toBe(0);
  });

  it('state minted for one tool cannot authorize another', () => {
    const key = new TextEncoder().encode(KEY);
    const args = mcpConfirmArgsDigest({ arguments: { target: 'data' } });
    const exp = Math.floor(Date.now() / 1000) + MCP_CONFIRM_STATE_TTL_SECONDS;
    const state = mintMcpConfirmState(
      key,
      { tool: 'purge', argsDigest: args, principal: '' },
      exp,
    );
    expect(
      verifyMcpConfirmState(
        key,
        state,
        { tool: 'widget purge', argsDigest: args, principal: '' },
        Math.floor(Date.now() / 1000),
      ),
    ).toBe('invalid');
  });

  it('state minted for one principal cannot authorize another', async () => {
    const { handler, executions } = mount();
    const r1 = await post(handler, {
      id: 1,
      headers: { Authorization: 'Bearer alice' },
    });
    const state = promptState(r1.result);

    const r2 = await post(handler, {
      id: 2,
      state,
      action: 'accept',
      headers: { Authorization: 'Bearer mallory' },
    });
    promptState(r2.result);
    expect(executions()).toBe(0);

    // The rightful principal is still able to redeem it.
    const r3 = await post(handler, {
      id: 3,
      state,
      action: 'accept',
      headers: { Authorization: 'Bearer alice' },
    });
    expect(r3.result.resultType).toBe('complete');
    expect(executions()).toBe(1);
  });

  it('argument key order does not change the digest', () => {
    const a = mcpConfirmArgsDigest({ arguments: { x: 1, y: { b: 2, a: 3 } } });
    const b = mcpConfirmArgsDigest({ arguments: { y: { a: 3, b: 2 }, x: 1 } });
    expect(a).toBe(b);
  });

  it('a state minted under a different key never verifies', () => {
    const binding: McpConfirmBinding = {
      tool: 'purge',
      argsDigest: mcpConfirmArgsDigest({}),
      principal: '',
    };
    const exp = Math.floor(Date.now() / 1000) + MCP_CONFIRM_STATE_TTL_SECONDS;
    const state = mintMcpConfirmState(
      new TextEncoder().encode('a-completely-different-shared-secret'),
      binding,
      exp,
    );
    expect(
      verifyMcpConfirmState(
        new TextEncoder().encode(KEY),
        state,
        binding,
        Math.floor(Date.now() / 1000),
      ),
    ).toBe('invalid');
  });

  it('any instance holding the same key verifies another instance state', async () => {
    const a = mount();
    const b = mount();
    const r1 = await post(a.handler, { id: 1, args: { target: 'data' } });
    const state = promptState(r1.result);

    // Redeemed against a DIFFERENT mount: statelessness, not a store.
    const r2 = await post(b.handler, {
      id: 2,
      args: { target: 'data' },
      state,
      action: 'accept',
    });
    expect(r2.result.resultType).toBe('complete');
    expect(b.executions()).toBe(1);
    expect(a.executions()).toBe(0);
  });
});

describe('verifyMcpConfirmState — structural defects', () => {
  const key = new TextEncoder().encode(KEY);
  const binding: McpConfirmBinding = {
    tool: 'purge',
    argsDigest: mcpConfirmArgsDigest({}),
    principal: '',
  };
  const now = Math.floor(Date.now() / 1000);

  for (const [label, state] of [
    ['empty', ''],
    ['two parts', 'v1.123'],
    ['four parts', 'v1.123.abc.def'],
    ['unknown version', 'v2.99999999999.AAAA'],
    ['non-decimal expiry', 'v1.0x40.AAAA'],
    ['exponent expiry', 'v1.1e10.AAAA'],
    ['padded expiry', 'v1. 123.AAAA'],
    ['undecodable tag', 'v1.99999999999.!!!!'],
  ] as const) {
    it(`${label} is invalid`, () => {
      expect(verifyMcpConfirmState(key, state, binding, now)).toBe('invalid');
    });
  }

  it('an authentic state past its expiry is expired, not invalid', () => {
    const state = mintMcpConfirmState(key, binding, now - 1);
    expect(verifyMcpConfirmState(key, state, binding, now)).toBe('expired');
  });

  it('a tampered expiry fails the MAC and is invalid, never expired', () => {
    const state = mintMcpConfirmState(key, binding, now - 1);
    const [, , mac] = state.split('.');
    const bumped = `v1.${now + 3600}.${mac}`;
    expect(verifyMcpConfirmState(key, bumped, binding, now)).toBe('invalid');
  });
});

describe('MRTR confirmation — the header gate still stands', () => {
  it('a client without elicitation capability falls back to the header gate', async () => {
    const { handler, executions } = mount();

    // No elicitation declared: refused at 428, NOT prompted.
    const refused = await post(handler, { meta: meta() });
    expect(refused.status).toBe(428);
    expect(refused.result.isError).toBe(true);
    expect(JSON.stringify(refused.result.content)).toContain(
      'confirmation required',
    );
    expect('inputRequests' in refused.result).toBe(false);
    expect(executions()).toBe(0);

    // With the header, the same client proceeds.
    const ok = await post(handler, {
      id: 2,
      meta: meta(),
      headers: { 'X-Confirm-Token': 'yes' },
    });
    expect(ok.status).toBe(200);
    expect(ok.result.resultType).toBe('complete');
    expect(executions()).toBe(1);
  });

  it('a url-only elicitation client cannot receive the form request', async () => {
    const { handler } = mount();
    const refused = await post(handler, { meta: meta({ url: {} }) });
    expect(refused.status).toBe(428);
    expect('inputRequests' in refused.result).toBe(false);
  });

  it('an elicitation object naming form is honored', async () => {
    const { handler } = mount();
    const { result } = await post(handler, { meta: meta({ form: {} }) });
    promptState(result);
  });

  for (const [label, declared] of [
    ['null', null],
    ['non-object', 'form'],
    ['array', []],
  ] as const) {
    it(`a ${label} elicitation declaration falls back to the header gate`, async () => {
      const { handler } = mount();
      const refused = await post(handler, { meta: meta(declared) });
      expect(refused.status).toBe(428);
    });
  }

  it('a mount with NO key keeps the header gate for everyone', async () => {
    const { handler, executions } = mount({ key: undefined });

    // Elicitation-capable, but there is no key to MAC state with.
    const refused = await post(handler, { meta: meta({}) });
    expect(refused.status).toBe(428);
    expect('inputRequests' in refused.result).toBe(false);
    expect('requestState' in refused.result).toBe(false);
    expect(executions()).toBe(0);

    const ok = await post(handler, {
      id: 2,
      meta: meta({}),
      headers: { 'X-Confirm-Token': 'yes' },
    });
    expect(ok.result.resultType).toBe('complete');
    expect(executions()).toBe(1);
  });

  it('rejects an empty confirmation key at mount time', () => {
    expect(() =>
      createMcpHandler(mrtrLockBridge().bridge, { confirmationKey: '' }),
    ).toThrow(/empty key/);
  });
});

describe('MRTR confirmation — leaves outside the gate', () => {
  it('a leaf that does not require confirmation is never prompted', async () => {
    const handler = createMcpHandler(modernLockBridge(), {
      confirmationKey: KEY,
    });
    const { status, result } = await post(handler, {
      tool: 'ping',
      meta: meta({}),
    });
    expect(status).toBe(200);
    expect(result.resultType).toBe('complete');
    expect('inputRequests' in result).toBe(false);
  });

  it('MRTR never relaxes the destructive ceiling', async () => {
    // `vault burn` is destructive AND requires confirmation, so it
    // travels the WHOLE loop and still meets the policy gate behind
    // it. Satisfying confirmation is not a way to bypass the ceiling.
    const { handler, executions } = mount();
    const r1 = await post(handler, { id: 1, tool: 'vault.burn' });
    const state = promptState(r1.result);

    const r2 = await post(handler, {
      id: 2,
      tool: 'vault.burn',
      state,
      action: 'accept',
    });
    expect(r2.status).toBe(200);
    expect(r2.result.resultType).toBe('complete');
    expect(r2.result.isError).toBe(true);
    expect(JSON.stringify(r2.result.content)).toContain('destructive');
    expect(executions()).toBe(0);
  });
});
