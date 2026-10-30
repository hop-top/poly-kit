/**
 * Legacy-era notifications and ping.
 *
 * A message with no `id` member is a JSON-RPC notification, which must
 * never be answered: the legacy path acknowledges it with HTTP 202 and
 * an empty body and does not process it. `ping` (a 2024-11-05 utility)
 * must be answered with an empty result. Both hold on the dual-era
 * mount and on a legacy-only mount.
 */

import { describe, expect, it } from 'vitest';

import { createMcpHandler } from './dispatch.js';
import { legacyLockBridge } from './testtree.js';
import type { McpBridge, McpMountOptions } from './types.js';

const MOUNTS: Record<string, McpMountOptions> = {
  'dual-era': {},
  'legacy-only': { specVersions: ['2024-11-05'] },
};

function countingBridge(): { bridge: McpBridge; calls: () => number } {
  const inner = legacyLockBridge();
  let n = 0;
  const bridge: McpBridge = {
    leaves: () => inner.leaves(),
    invoke: (inv) => {
      n++;
      return inner.invoke(inv);
    },
  };
  return { bridge, calls: () => n };
}

describe.each(Object.entries(MOUNTS))('legacy era (%s mount)', (_name, opts) => {
  it.each([
    '{"jsonrpc":"2.0","method":"notifications/initialized"}',
    '{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}',
    '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"ping"}}',
  ])('does not answer the notification %s', async (body) => {
    const { bridge, calls } = countingBridge();
    const handler = createMcpHandler(bridge, opts);
    const res = await handler({ method: 'POST', body });
    expect(res.status).toBe(202);
    expect(res.body).toBe('');
    expect(calls()).toBe(0);
  });

  it.each([
    ['{"jsonrpc":"2.0","id":7,"method":"ping"}', '{"jsonrpc":"2.0","id":7,"result":{}}\n'],
    ['{"jsonrpc":"2.0","id":"p","method":"ping","params":{}}', '{"jsonrpc":"2.0","id":"p","result":{}}\n'],
    ['{"jsonrpc":"2.0","id":null,"method":"ping"}', '{"jsonrpc":"2.0","id":null,"result":{}}\n'],
  ])('answers ping %s with an empty result', async (body, want) => {
    const handler = createMcpHandler(legacyLockBridge(), opts);
    const res = await handler({ method: 'POST', body });
    expect(res.status).toBe(200);
    expect(res.body).toBe(want);
  });
});
