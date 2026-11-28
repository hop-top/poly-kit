import { Command } from 'commander';
import { createMcpHandler, commanderBridge } from '@hop-top/kit/mcp';

const root = new Command('app');
root.command('ping').description('Ping the server');

const bridge = commanderBridge(root, {
  run: async () => ({ stdout: 'pong\n', exitCode: 0 }),
});
const handler = createMcpHandler(bridge, {
  serverInfo: { name: 'app', version: '0.1' },
});

const res = await handler({
  method: 'POST',
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/list' }),
});
console.log(res.status);
console.log(JSON.parse(String(res.body)).result.tools.map((t: { name: string }) => t.name));
