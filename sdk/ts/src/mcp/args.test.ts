/**
 * Positional arguments over MCP (mcp/args), on both protocol eras:
 * the "args" property tools/list publishes for a commander leaf's
 * declared arguments, and the mapping of a tools/call "args" array
 * back onto Invocation.args. Mirrors the Go reference's
 * surface_mcp_args_test.go case for case, wording included.
 */

import { Command } from 'commander';
import { describe, expect, it } from 'vitest';

import { commanderBridge, type AnnotatedCommand } from './bridge.js';
import { createMcpHandler } from './dispatch.js';
import type { Invocation, InvokeResult } from './types.js';

/**
 * root
 * └── item
 *     ├── add      <name> [note]; --count
 *     ├── pin      <id>; an --args flag holds the name
 *     ├── approve  <name>; kit/requires-confirmation
 *     └── tag      <files...> variadic, required
 */
function argsTree(): Command {
  const root = new Command('root');
  const item = root.command('item');
  const ann = (cmd: Command, a: Record<string, string>) => {
    (cmd as AnnotatedCommand).annotations = a;
    return cmd;
  };
  ann(
    item.command('add').description('Add an item').argument('<name>').argument('[note]')
      .option('--count <n>', 'how many'),
    { 'kit/side-effect': 'write' },
  );
  ann(
    item.command('pin').description('Pin an item').argument('<id>')
      .option('--args <v>', 'extra arguments'),
    { 'kit/side-effect': 'write' },
  );
  ann(item.command('approve').description('Approve an item').argument('<name>'), {
    'kit/side-effect': 'write',
    'kit/requires-confirmation': 'true',
  });
  item.command('tag').description('Tag files').argument('<files...>');
  return root;
}

function echo(inv: Invocation): InvokeResult {
  const verb = inv.path[inv.path.length - 1];
  let extra = '';
  if (inv.flags?.count !== undefined) extra = ` x${String(inv.flags.count)}`;
  if (inv.flags?.args !== undefined) extra = ` --args=${String(inv.flags.args)}`;
  return { stdout: `${verb} ${JSON.stringify(inv.args ?? [])}${extra}\n`, exitCode: 0 };
}

const MODERN_META = {
  'io.modelcontextprotocol/clientCapabilities': {},
  'io.modelcontextprotocol/protocolVersion': '2026-07-28',
};

interface Tool {
  name: string;
  description: string;
  inputSchema: { properties: Record<string, Record<string, unknown>>; required?: string[] };
}

interface Era {
  name: string;
  list(): Promise<Record<string, Tool>>;
  call(
    tool: string,
    args: Record<string, unknown>,
  ): Promise<{ status: number; text: string; isError: boolean }>;
}

function eras(): Era[] {
  const handler = createMcpHandler(commanderBridge(argsTree(), { run: echo }));
  const send = async (body: unknown, headers: Record<string, string> = {}) => {
    const res = await handler({ method: 'POST', headers, body: JSON.stringify(body) });
    return { status: res.status, body: JSON.parse(res.body) as { result: Record<string, unknown> } };
  };
  const tools = (r: Record<string, unknown>) =>
    Object.fromEntries((r.tools as Tool[]).map((t) => [t.name, t]));
  const outcome = (status: number, r: Record<string, unknown>) => ({
    status,
    text: (r.content as Array<{ text: string }>).map((c) => c.text).join(''),
    isError: r.isError === true,
  });
  return [
    {
      name: '2024-11-05',
      async list() {
        const { body } = await send({ jsonrpc: '2.0', id: 1, method: 'tools/list', params: {} });
        return tools(body.result);
      },
      async call(tool, args) {
        const { status, body } = await send({
          jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: tool, arguments: args },
        });
        return outcome(status, body.result);
      },
    },
    {
      name: '2026-07-28',
      async list() {
        const { body } = await send(
          { jsonrpc: '2.0', id: 1, method: 'tools/list', params: { _meta: MODERN_META } },
          { 'MCP-Protocol-Version': '2026-07-28', 'Mcp-Method': 'tools/list' },
        );
        return tools(body.result);
      },
      async call(tool, args) {
        const { status, body } = await send(
          {
            jsonrpc: '2.0', id: 1, method: 'tools/call',
            params: { name: tool, arguments: args, _meta: MODERN_META },
          },
          { 'MCP-Protocol-Version': '2026-07-28', 'Mcp-Method': 'tools/call', 'Mcp-Name': tool },
        );
        return outcome(status, body.result);
      },
    },
  ];
}

describe.each(eras())('positional args over MCP ($name)', (era) => {
  it('tools/list publishes declared positionals as one args array', async () => {
    const tools = await era.list();

    const add = tools['item.add']!;
    expect(add.description).toBe('Add an item');
    expect(add.inputSchema.properties.args).toEqual({
      type: 'array',
      items: { type: 'string' },
      description: 'Positional arguments in order: name, note?',
      minItems: 1,
    });
    expect(add.inputSchema.properties).toHaveProperty('count');
    expect(add.inputSchema.required).toEqual(['args']);

    const pin = tools['item.pin']!;
    expect(pin.inputSchema.properties.args?.type).toBe('string');
    expect(pin.inputSchema.required).toBeUndefined();
    expect(pin.description).toBe(
      'Pin an item Takes positional arguments that cannot be passed over MCP: its --args flag holds the "args" property.',
    );

    expect(tools['item.tag']!.inputSchema.properties.args).toEqual({
      type: 'array',
      items: { type: 'string' },
      description: 'Positional arguments in order: files',
      minItems: 1,
    });
  });

  it('tools/call maps args onto positionals in order', async () => {
    let r = await era.call('item.add', { args: ['bolt', 'spare'], count: '2' });
    expect(r).toEqual({ status: 200, text: 'add ["bolt","spare"] x2\n', isError: false });

    r = await era.call('item.add', { args: ['nut'] });
    expect(r.text).toBe('add ["nut"]\n');

    r = await era.call('item.tag', { args: ['a', 'b', 'c'] });
    expect(r.text).toBe('tag ["a","b","c"]\n');
  });

  it('refuses missing or malformed args', async () => {
    for (const args of [{ count: '2' }, { args: [] }]) {
      expect(await era.call('item.add', args)).toEqual({
        status: 200, text: 'missing required argument: name', isError: true,
      });
    }
    for (const args of [{ args: 'bolt' }, { args: [1] }]) {
      const r = await era.call('item.add', args);
      expect(r.isError).toBe(true);
      expect(r.text).toBe('"args" must be an array of strings');
    }
  });

  it('checks arguments before the confirmation gate', async () => {
    let r = await era.call('item.approve', {});
    expect(r).toEqual({ status: 200, text: 'missing required argument: name', isError: true });

    r = await era.call('item.approve', { args: ['bolt'] });
    expect(r.status).toBe(428);
    expect(r.isError).toBe(true);
    expect(r.text).toContain('confirmation required');
  });

  it('an --args flag keeps the name and gets the value as a flag', async () => {
    const r = await era.call('item.pin', { args: 'x' });
    expect(r).toEqual({ status: 200, text: 'pin [] --args=x\n', isError: false });
  });
});

describe('commanderBridge without positionals', () => {
  it('publishes no args property and passes no Invocation.args', async () => {
    const root = new Command('root');
    root.command('ping').description('Ping').option('--count <n>', 'how many');
    let seen: Invocation | undefined;
    const handler = createMcpHandler(
      commanderBridge(root, {
        run: (inv) => {
          seen = inv;
          return { stdout: '', exitCode: 0 };
        },
      }),
    );
    const list = await handler({
      method: 'POST', headers: {}, body: '{"jsonrpc":"2.0","id":1,"method":"tools/list"}',
    });
    expect(list.body).toContain(
      '"inputSchema":{"properties":{"count":{"description":"how many","type":"string"}},"type":"object"}',
    );
    await handler({
      method: 'POST', headers: {},
      body: '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping","arguments":{"count":"3"}}}',
    });
    expect(seen?.args).toBeUndefined();
    expect(seen?.flags).toEqual({ count: '3' });
  });
});
