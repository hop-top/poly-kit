/**
 * Coverage for the commander bridge adapter: leaf enumeration, flag
 * schema derivation, annotation classification, and the destructive
 * ceiling on the invoke path.
 */

import { Command, Option } from 'commander';
import { describe, expect, it } from 'vitest';

import { commanderBridge, type AnnotatedCommand } from './bridge.js';
import { createMcpHandler } from './dispatch.js';
import { SURFACE_MCP, type InvokeResult } from './types.js';

function annotate(cmd: Command, ann: Record<string, string>): Command {
  (cmd as AnnotatedCommand).annotations = ann;
  return cmd;
}

/** root ├── widget {add,delete} ├── secret ├── ping */
function buildTree(): Command {
  const root = new Command('root');

  const widget = new Command('widget');
  const add = new Command('add').description('Add a widget');
  add.requiredOption('--name <n>', 'widget name');
  add.option('--count <n>', 'widget count');
  add.option('--force', 'force flag');
  add.option('--tag <t...>', 'tag list');
  const hidden = new Option('--hidden-flag <v>', 'should be hidden').hideHelp();
  add.addOption(hidden);
  annotate(add, { 'kit/side-effect': 'write' });
  widget.addCommand(add);

  const del = new Command('delete').description('Delete a widget');
  annotate(del, { 'kit/side-effect': 'destructive' });
  widget.addCommand(del);
  root.addCommand(widget);

  const secret = new Command('secret').description('Locked');
  annotate(secret, { 'kit/auth-required': 'true' });
  root.addCommand(secret);

  const ping = new Command('ping').description('Ping the server');
  annotate(ping, { 'kit/side-effect': 'read' });
  root.addCommand(ping);

  return root;
}

const run = (): InvokeResult => ({ stdout: 'ok\n', exitCode: 0 });

describe('commanderBridge', () => {
  it('enumerates leaves as dotted paths, excluding groups and the root', () => {
    const bridge = commanderBridge(buildTree(), { run });
    expect(bridge.leaves().map((l) => l.path.join('.'))).toEqual([
      'widget.add',
      'widget.delete',
      'secret',
      'ping',
    ]);
  });

  it('derives a JSON Schema from options, excluding hidden ones', () => {
    const bridge = commanderBridge(buildTree(), { run });
    const add = bridge.leaves().find((l) => l.path.join('.') === 'widget.add');
    expect(add?.properties).toEqual({
      name: { type: 'string', description: 'widget name' },
      count: { type: 'string', description: 'widget count' },
      force: { type: 'boolean', description: 'force flag' },
      tag: { type: 'array', description: 'tag list', items: { type: 'string' } },
    });
    expect(add?.required).toEqual(['name']);
    expect(add?.properties).not.toHaveProperty('hidden-flag');
  });

  it('types a negated flag as a boolean under its own long name', () => {
    const root = new Command('root');
    root.command('find').option('--no-color', 'plain output');
    const bridge = commanderBridge(root, { run });
    expect(bridge.leaves()[0]?.properties).toEqual({
      'no-color': { type: 'boolean', description: 'plain output' },
    });
  });

  it('keeps a flag and its negation as two booleans', () => {
    const root = new Command('root');
    root.command('find').option('--cache', 'use cache').option('--no-cache', 'skip cache');
    const bridge = commanderBridge(root, { run });
    expect(bridge.leaves()[0]?.properties).toEqual({
      cache: { type: 'boolean', description: 'use cache' },
      'no-cache': { type: 'boolean', description: 'skip cache' },
    });
  });

  it('types a repeatable accumulator option as an array of strings', () => {
    const root = new Command('root');
    root
      .command('find')
      .option('-m, --match <p>', 'match pattern', (v: string, p: string[]) => p.concat([v]), []);
    const bridge = commanderBridge(root, { run });
    expect(bridge.leaves()[0]?.properties).toEqual({
      match: {
        type: 'array',
        description: 'match pattern',
        items: { type: 'string' },
        default: [],
      },
    });
  });

  it('keeps a custom-parsed scalar option a string', () => {
    const root = new Command('root');
    root.command('find').option('--limit <n>', 'max rows', (v: string) => Number(v));
    const bridge = commanderBridge(root, { run });
    expect(bridge.leaves()[0]?.properties).toEqual({
      limit: { type: 'string', description: 'max rows' },
    });
  });

  it('publishes choices as enum and string defaults as default', () => {
    const root = new Command('root');
    root
      .command('find')
      .addOption(new Option('--color <c>', 'tint').choices(['red', 'blue']).default('red'))
      .addOption(new Option('--tag <t...>', 'tags').choices(['a', 'b']))
      .option('--sort <k>', 'sort key', 'name');
    const bridge = commanderBridge(root, { run });
    expect(bridge.leaves()[0]?.properties).toEqual({
      color: { type: 'string', description: 'tint', enum: ['red', 'blue'], default: 'red' },
      tag: { type: 'array', description: 'tags', items: { type: 'string', enum: ['a', 'b'] } },
      sort: { type: 'string', description: 'sort key', default: 'name' },
    });
  });

  it('omits a default whose type does not match the schema type', () => {
    const root = new Command('root');
    root
      .command('find')
      .option('--limit <n>', 'max rows', (v: string) => Number(v), 10)
      .option('--fast', 'go fast', false);
    const bridge = commanderBridge(root, { run });
    expect(bridge.leaves()[0]?.properties).toEqual({
      limit: { type: 'string', description: 'max rows' },
      fast: { type: 'boolean', description: 'go fast', default: false },
    });
  });

  it('classifies kit annotations into the SafetyClass', () => {
    const bridge = commanderBridge(buildTree(), { run });
    const byName = (n: string) =>
      bridge.leaves().find((l) => l.path.join('.') === n);
    expect(byName('widget.delete')?.class?.destructive).toBe(true);
    expect(byName('secret')?.class?.authRequired).toBe(true);
    expect(byName('ping')?.class?.destructive).toBeUndefined();
  });

  it('blocks a destructive leaf on MCP under the default policy', async () => {
    const bridge = commanderBridge(buildTree(), { run });
    await expect(
      bridge.invoke({
        path: ['widget', 'delete'],
        meta: { surface: SURFACE_MCP, requestedAt: new Date() },
      }),
    ).rejects.toThrow(/destructive command blocked on this surface/);
  });

  it('allows the same leaf once MCP is named in allowDestructiveOn', async () => {
    const bridge = commanderBridge(buildTree(), {
      run,
      policy: { allowDestructiveOn: [SURFACE_MCP] },
    });
    await expect(
      bridge.invoke({
        path: ['widget', 'delete'],
        meta: { surface: SURFACE_MCP, requestedAt: new Date() },
      }),
    ).resolves.toEqual({ stdout: 'ok\n', exitCode: 0 });
  });

  it('rejects an unknown leaf path', async () => {
    const bridge = commanderBridge(buildTree(), { run });
    await expect(
      bridge.invoke({
        path: ['nope'],
        meta: { surface: SURFACE_MCP, requestedAt: new Date() },
      }),
    ).rejects.toThrow(/unknown command/);
  });

  it('serves a real tools/list through the MCP handler', async () => {
    const handler = createMcpHandler(commanderBridge(buildTree(), { run }));
    const res = await handler({
      method: 'POST',
      headers: {},
      body: '{"jsonrpc":"2.0","id":1,"method":"tools/list"}',
    });
    expect(res.status).toBe(200);
    const body = JSON.parse(res.body) as {
      result: { tools: Array<{ name: string }> };
    };
    expect(body.result.tools.map((t) => t.name)).toEqual([
      'widget.add',
      'widget.delete',
      'secret',
      'ping',
    ]);
  });

  it('renders a destructive block as an isError result at HTTP 200', async () => {
    const handler = createMcpHandler(commanderBridge(buildTree(), { run }));
    const res = await handler({
      method: 'POST',
      headers: {},
      body: '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"widget.delete"}}',
    });
    expect(res.status).toBe(200);
    expect(res.body).toContain('"isError":true');
    expect(res.body).toContain(
      'destructive command blocked on this surface: widget delete on mcp',
    );
  });
});
