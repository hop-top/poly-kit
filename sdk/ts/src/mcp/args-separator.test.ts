/**
 * Positional arguments under a non-default toolNameSeparator, on both
 * eras over HTTP and over serveStdio: the separator only renames the
 * tool, the "args" array still reaches Invocation.args in order.
 */

import { PassThrough } from 'node:stream';
import { Command } from 'commander';
import { describe, expect, it } from 'vitest';

import { commanderBridge } from './bridge.js';
import { createMcpHandler } from './dispatch.js';
import { serveStdio } from './stdio.js';
import type { Invocation, InvokeResult, McpBridge } from './types.js';

const MODERN_META = {
  'io.modelcontextprotocol/clientCapabilities': {},
  'io.modelcontextprotocol/protocolVersion': '2026-07-28',
};

/** root -> item -> add <name> [note] --count <n> */
function bridge(): McpBridge {
  const root = new Command('root');
  root
    .command('item')
    .command('add')
    .description('Add an item')
    .argument('<name>')
    .argument('[note]')
    .option('--count <n>', 'how many');
  const run = (inv: Invocation): InvokeResult => ({
    stdout: `${inv.path.join(' ')} ${JSON.stringify(inv.args ?? [])} ${JSON.stringify(inv.flags ?? {})}\n`,
    exitCode: 0,
  });
  return commanderBridge(root, { run });
}

function text(result: Record<string, unknown>): string {
  return (result.content as Array<{ text: string }>).map((c) => c.text).join('');
}

describe('positional args with toolNameSeparator', () => {
  const handler = createMcpHandler(bridge(), { toolNameSeparator: '_' });
  const post = (body: unknown, headers: Record<string, string> = {}) =>
    handler({ method: 'POST', headers, body: JSON.stringify(body) });

  it('publishes the args property under the separated name', async () => {
    const res = await post({ jsonrpc: '2.0', id: 1, method: 'tools/list' });
    const tools = JSON.parse(res.body).result.tools as Array<{
      name: string;
      inputSchema: { properties: Record<string, unknown>; required?: string[] };
    }>;
    const add = tools.find((t) => t.name === 'item_add');
    expect(add).toBeDefined();
    expect(add?.inputSchema.properties.args).toMatchObject({ type: 'array', minItems: 1 });
    expect(add?.inputSchema.required).toContain('args');
  });

  it('splits args out on a legacy call', async () => {
    const res = await post({
      jsonrpc: '2.0',
      id: 2,
      method: 'tools/call',
      params: { name: 'item_add', arguments: { args: ['bolt', '-x'], count: '2' } },
    });
    const body = JSON.parse(res.body);
    expect(body.result.isError).toBe(false);
    expect(text(body.result)).toBe('item add ["bolt","-x"] {"count":"2"}\n');
  });

  it('splits args out on a modern call', async () => {
    const res = await post(
      {
        jsonrpc: '2.0',
        id: 3,
        method: 'tools/call',
        params: { name: 'item_add', arguments: { args: ['bolt'] }, _meta: MODERN_META },
      },
      {
        'MCP-Protocol-Version': '2026-07-28',
        'Mcp-Method': 'tools/call',
        'Mcp-Name': 'item_add',
      },
    );
    const body = JSON.parse(res.body);
    expect(body.error).toBeUndefined();
    expect(body.result.isError).toBe(false);
    expect(text(body.result)).toBe('item add ["bolt"] {}\n');
  });

  it('refuses a missing required argument on both eras', async () => {
    const legacy = JSON.parse(
      (
        await post({
          jsonrpc: '2.0',
          id: 4,
          method: 'tools/call',
          params: { name: 'item_add', arguments: {} },
        })
      ).body,
    );
    expect(legacy.result.isError).toBe(true);
    expect(text(legacy.result)).toContain('missing required argument: name');

    const modern = JSON.parse(
      (
        await post(
          {
            jsonrpc: '2.0',
            id: 5,
            method: 'tools/call',
            params: { name: 'item_add', arguments: {}, _meta: MODERN_META },
          },
          {
            'MCP-Protocol-Version': '2026-07-28',
            'Mcp-Method': 'tools/call',
            'Mcp-Name': 'item_add',
          },
        )
      ).body,
    );
    expect(modern.result.isError).toBe(true);
    expect(text(modern.result)).toContain('missing required argument: name');
  });

  it('carries args over serveStdio on both eras', async () => {
    const input = new PassThrough();
    const output = new PassThrough();
    let out = '';
    output.on('data', (c: Buffer) => (out += c.toString('utf8')));
    const server = serveStdio(bridge(), {
      input,
      output,
      log: new PassThrough(),
      mount: { toolNameSeparator: '_' },
    });
    input.write(
      `${JSON.stringify({
        jsonrpc: '2.0',
        id: 1,
        method: 'tools/call',
        params: { name: 'item_add', arguments: { args: ['bolt', 'spare'] } },
      })}\n`,
    );
    input.write(
      `${JSON.stringify({
        jsonrpc: '2.0',
        id: 2,
        method: 'tools/call',
        params: { name: 'item_add', arguments: { args: ['nut'] }, _meta: MODERN_META },
      })}\n`,
    );
    input.end();
    await server.done;
    const got = new Map<number, Record<string, unknown>>();
    for (const line of out.split('\n').filter((l) => l !== '')) {
      const m = JSON.parse(line) as { id: number; result: Record<string, unknown> };
      got.set(m.id, m.result);
    }
    expect(text(got.get(1) ?? {})).toBe('item add ["bolt","spare"] {}\n');
    expect(got.get(2)).toMatchObject({ resultType: 'complete', isError: false });
    expect(text(got.get(2) ?? {})).toBe('item add ["nut"] {}\n');
  });
});
