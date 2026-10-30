/**
 * The toolNameSeparator mount option: tool names join the leaf path
 * with it in tools/list, resolve back through it on tools/call (both
 * eras), and a separator a command name contains fails the mount.
 */

import { describe, expect, it } from 'vitest';

import { createMcpHandler } from './dispatch.js';
import { ElicitationConfirmGate } from './modern-confirm.js';
import { legacyLockBridge, modernLockBridge } from './testtree.js';
import {
  pathFromToolName,
  resolveMcpConfig,
  toolName,
  type McpBridge,
} from './types.js';

const MODERN_META = {
  'io.modelcontextprotocol/clientCapabilities': {},
  'io.modelcontextprotocol/protocolVersion': '2026-07-28',
};

function post(body: unknown, headers: Record<string, string> = {}) {
  return { method: 'POST', headers, body: JSON.stringify(body) };
}

async function listNames(
  bridge: McpBridge,
  opts: Parameters<typeof createMcpHandler>[1],
): Promise<string[]> {
  const res = await createMcpHandler(bridge, opts)(
    post({ jsonrpc: '2.0', id: 1, method: 'tools/list' }),
  );
  const tools = JSON.parse(res.body).result.tools as Array<{ name: string }>;
  return tools.map((t) => t.name);
}

/** A bridge whose leaf `data_sync` and leaf `data sync` read alike under "_". */
function collidingBridge(): McpBridge {
  return {
    leaves: () => [
      { path: ['data', 'sync'], description: 'Sync', properties: {} },
      { path: ['data_sync'], description: 'Sync too', properties: {} },
    ],
    invoke: () => ({ exitCode: 0, stdout: '' }),
  };
}

describe('toolName / pathFromToolName', () => {
  it('join and split on "." by default', () => {
    expect(toolName(['widget', 'add'])).toBe('widget.add');
    expect(pathFromToolName('widget.add')).toEqual(['widget', 'add']);
  });

  it('join and split on a given separator symmetrically', () => {
    for (const sep of ['_', '-', '__']) {
      const name = toolName(['widget', 'add'], sep);
      expect(name).toBe(`widget${sep}add`);
      expect(pathFromToolName(name, sep)).toEqual(['widget', 'add']);
    }
    expect(pathFromToolName('', '_')).toEqual([]);
  });
});

describe('toolNameSeparator mount option', () => {
  it('defaults to "."', async () => {
    expect(resolveMcpConfig().toolNameSeparator).toBe('.');
    expect(await listNames(legacyLockBridge(), {})).toContain('widget.add');
  });

  it('renames listed tools', async () => {
    for (const sep of ['_', '-']) {
      const names = await listNames(legacyLockBridge(), { toolNameSeparator: sep });
      expect(names).toContain(`widget${sep}add`);
      expect(names).toContain(`widget${sep}delete`);
      expect(names).toContain('ping');
      expect(names).not.toContain('widget.add');
    }
  });

  it('resolves a legacy tools/call through the separator', async () => {
    const handler = createMcpHandler(legacyLockBridge(), { toolNameSeparator: '_' });
    const call = (name: string) =>
      handler(
        post({
          jsonrpc: '2.0',
          id: 2,
          method: 'tools/call',
          params: { name, arguments: { name: 'bolt' } },
        }),
      );
    const ok = JSON.parse((await call('widget_add')).body);
    expect(ok.result.isError).toBe(false);
    const dotted = JSON.parse((await call('widget.add')).body);
    expect(dotted.error.message).toContain('unknown tool: widget.add');
  });

  it('resolves a modern tools/call through the separator', async () => {
    const handler = createMcpHandler(modernLockBridge(), { toolNameSeparator: '-' });
    const res = await handler(
      post(
        {
          jsonrpc: '2.0',
          id: 3,
          method: 'tools/call',
          params: { name: 'widget-add', arguments: { name: 'bolt' }, _meta: MODERN_META },
        },
        {
          'MCP-Protocol-Version': '2026-07-28',
          'Mcp-Method': 'tools/call',
          'Mcp-Name': 'widget-add',
        },
      ),
    );
    const body = JSON.parse(res.body);
    expect(body.error).toBeUndefined();
    expect(body.result.isError).toBe(false);
  });

  it('names the tool under the separator in the confirmation prompt', () => {
    const gate = new ElicitationConfirmGate(new TextEncoder().encode('k'), '_');
    const out = gate.inputRequired(
      { path: ['vault', 'burn'], description: '', properties: {} },
      { tool: 'vault burn', argsDigest: '', principal: '' },
    ) as { inputRequests: { confirm: { params: { message: string } } } };
    expect(out.inputRequests.confirm.params.message).toBe(
      'Approve execution of "vault_burn"?',
    );
  });

  it('refuses an empty separator at mount', () => {
    expect(() => createMcpHandler(legacyLockBridge(), { toolNameSeparator: '' })).toThrow(
      'cmdsurface: toolNameSeparator: empty separator',
    );
  });

  it('refuses a separator a command name contains', () => {
    expect(() => createMcpHandler(collidingBridge(), { toolNameSeparator: '_' })).toThrow(
      'cmdsurface: toolNameSeparator: command "data_sync" in "data_sync" contains separator "_"',
    );
  });

  it('accepts a separator no command name contains', () => {
    expect(() => createMcpHandler(collidingBridge(), { toolNameSeparator: '-' })).not.toThrow();
  });
});
