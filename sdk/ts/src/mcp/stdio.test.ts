/**
 * The stdio binding: newline framing, notifications never answered,
 * ping, parse / batch refusals, concurrent requests, both eras with no
 * header layer, and a clean end of session.
 */

import { PassThrough } from 'node:stream';
import { describe, expect, it } from 'vitest';

import { createMcpHandler, type McpHandler } from './dispatch.js';
import { serveStdio, type McpStdioServer } from './stdio.js';
import { legacyLockBridge } from './testtree.js';
import type { McpBridge, McpMountOptions } from './types.js';

const MODERN_META =
  '"_meta":{"io.modelcontextprotocol/clientCapabilities":{},' +
  '"io.modelcontextprotocol/protocolVersion":"2026-07-28"}';

interface Session {
  server: McpStdioServer;
  input: PassThrough;
  /** Every line written to the output stream, in order. */
  lines: () => string[];
  /** Everything written to the log stream. */
  log: () => string;
}

function session(
  target: McpBridge | McpHandler,
  mount?: McpMountOptions,
): Session {
  const input = new PassThrough();
  const output = new PassThrough();
  const log = new PassThrough();
  let out = '';
  let err = '';
  output.on('data', (c: Buffer) => (out += c.toString('utf8')));
  log.on('data', (c: Buffer) => (err += c.toString('utf8')));
  const server = serveStdio(target, { input, output, log, mount });
  return {
    server,
    input,
    lines: () => out.split('\n').filter((l) => l !== ''),
    log: () => err,
  };
}

/** Writes each message as one line, ends input, waits for the session. */
async function run(s: Session, ...messages: string[]): Promise<string[]> {
  for (const m of messages) s.input.write(`${m}\n`);
  s.input.end();
  await s.server.done;
  return s.lines();
}

function byId(lines: string[]): Map<unknown, Record<string, unknown>> {
  const out = new Map<unknown, Record<string, unknown>>();
  for (const l of lines) {
    const m = JSON.parse(l) as Record<string, unknown>;
    out.set(m.id, m);
  }
  return out;
}

/** A bridge whose `slow` leaf answers only after `fast` has. */
function orderedBridge(): McpBridge {
  let releaseSlow: () => void = () => {};
  const fastDone = new Promise<void>((r) => (releaseSlow = r));
  return {
    leaves: () => [
      { path: ['slow'], description: 'Slow', properties: {} },
      { path: ['fast'], description: 'Fast', properties: {} },
    ],
    async invoke(inv) {
      if (inv.path[0] === 'slow') {
        await fastDone;
        return { stdout: 'slow\n', exitCode: 0 };
      }
      setTimeout(releaseSlow, 5);
      return { stdout: 'fast\n', exitCode: 0 };
    },
  };
}

describe('serveStdio', () => {
  it('runs the legacy handshake and never answers a notification', async () => {
    const lines = await run(
      session(legacyLockBridge()),
      '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"pipe","version":"0"}}}',
      '{"jsonrpc":"2.0","method":"notifications/initialized"}',
      '{"jsonrpc":"2.0","id":2,"method":"tools/list"}',
      '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ping"}}',
    );
    expect(lines).toHaveLength(3);
    const got = byId(lines);
    expect([...got.keys()].sort()).toEqual([1, 2, 3]);
    expect(got.get(1)?.result).toMatchObject({ protocolVersion: '2024-11-05' });
    expect(JSON.stringify(got.get(3)?.result)).toContain('pong');
  });

  it('answers ping with an empty result', async () => {
    const lines = await run(session(legacyLockBridge()), '{"jsonrpc":"2.0","id":"p1","method":"ping"}');
    expect(lines).toEqual(['{"jsonrpc":"2.0","id":"p1","result":{}}']);
  });

  it('drops every notification, even a malformed or failing one', async () => {
    const lines = await run(
      session(legacyLockBridge()),
      '{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}',
      '{"jsonrpc":"1.0","method":"notifications/initialized"}',
      '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"ping"}}',
      '{"jsonrpc":"2.0","method":"nope"}',
    );
    expect(lines).toEqual([]);
  });

  it('answers unparseable input with -32700 and id null', async () => {
    const lines = await run(session(legacyLockBridge()), '{not json');
    expect(lines).toEqual([
      '{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error: invalid character \'n\' looking for beginning of object key string"}}',
    ]);
  });

  it('refuses a batch and a non-object message with -32600 and id null', async () => {
    const lines = await run(
      session(legacyLockBridge()),
      '[{"jsonrpc":"2.0","id":1,"method":"ping"}]',
      '42',
    );
    expect(lines).toEqual([
      '{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"invalid request: batch requests are not supported"}}',
      '{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"invalid request: not a JSON-RPC object"}}',
    ]);
  });

  it('answers a request without a method with -32600 and its id', async () => {
    const lines = await run(session(legacyLockBridge()), '{"jsonrpc":"2.0","id":9}');
    expect(lines).toEqual([
      '{"jsonrpc":"2.0","id":9,"error":{"code":-32600,"message":"invalid request: missing method"}}',
    ]);
  });

  it('ignores a client response on stdout and logs it to the log stream', async () => {
    const s = session(legacyLockBridge());
    const lines = await run(s, '{"jsonrpc":"2.0","id":1,"result":{}}');
    expect(lines).toEqual([]);
    expect(s.log()).toContain('ignored a JSON-RPC response');
  });

  it('skips blank lines and accepts CRLF framing', async () => {
    const s = session(legacyLockBridge());
    s.input.write('\n   \n{"jsonrpc":"2.0","id":1,"method":"ping"}\r\n');
    s.input.end();
    await s.server.done;
    expect(s.lines()).toEqual(['{"jsonrpc":"2.0","id":1,"result":{}}']);
  });

  it('runs requests concurrently; replies arrive out of order, each with its id', async () => {
    const lines = await run(
      session(orderedBridge()),
      '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"slow"}}',
      '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"fast"}}',
    );
    expect(lines.map((l) => (JSON.parse(l) as { id: number }).id)).toEqual([2, 1]);
    expect(lines[0]).toContain('fast');
    expect(lines[1]).toContain('slow');
  });

  it('serves 2026-07-28 from the body alone: no header layer on stdio', async () => {
    const lines = await run(
      session(legacyLockBridge()),
      `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{${MODERN_META}}}`,
      `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping",${MODERN_META}}}`,
      `{"jsonrpc":"2.0","id":3,"method":"ping","params":{${MODERN_META}}}`,
    );
    const got = byId(lines);
    expect(got.get(1)?.result).toMatchObject({
      resultType: 'complete',
      supportedVersions: ['2026-07-28'],
    });
    expect(got.get(2)?.result).toMatchObject({ resultType: 'complete', isError: false });
    // 2026-07-28 removed ping: a modern ping is an unknown method.
    expect(got.get(3)?.error).toMatchObject({ code: -32601 });
  });

  it('keeps the remaining modern validation (unsupported version)', async () => {
    const lines = await run(
      session(legacyLockBridge()),
      '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2099-01-01"}}}',
    );
    expect(JSON.parse(lines[0] ?? '{}')).toMatchObject({ id: 1, error: { code: -32022 } });
  });

  it('reuses a createMcpHandler handler, mount options included', async () => {
    const handler = createMcpHandler(legacyLockBridge(), {
      serverInfo: { name: 'stdio-tool', version: '9.9.9' },
    });
    const lines = await run(
      session(handler),
      '{"jsonrpc":"2.0","id":1,"method":"initialize"}',
      `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{${MODERN_META}}}`,
    );
    const got = byId(lines);
    expect(got.get(1)?.result).toMatchObject({ serverInfo: { name: 'stdio-tool', version: '9.9.9' } });
    expect(got.get(2)?.result).toMatchObject({ resultType: 'complete' });
  });

  it('applies mount options when serving a bridge', async () => {
    const lines = await run(
      session(legacyLockBridge(), { specVersions: ['2026-07-28'] }),
      '{"jsonrpc":"2.0","id":1,"method":"initialize"}',
    );
    expect(lines[0]).toContain('supported protocol versions: 2026-07-28');
  });

  it('refuses mount options alongside a handler', () => {
    expect(() =>
      serveStdio(createMcpHandler(legacyLockBridge()), {
        input: new PassThrough(),
        output: new PassThrough(),
        mount: {},
      }),
    ).toThrow(TypeError);
  });

  it('drives a foreign handler as an HTTP-shaped POST', async () => {
    const inner = createMcpHandler(legacyLockBridge());
    const foreign: McpHandler = (req) => inner(req);
    const lines = await run(
      session(foreign),
      '{"jsonrpc":"2.0","id":1,"method":"ping"}',
      '{"jsonrpc":"2.0","method":"notifications/initialized"}',
    );
    expect(lines).toEqual(['{"jsonrpc":"2.0","id":1,"result":{}}']);
  });

  it('answers -32603 when the handler throws, and logs the cause', async () => {
    const s = session((() => Promise.reject(new Error('boom'))) as McpHandler);
    const lines = await run(s, '{"jsonrpc":"2.0","id":4,"method":"ping"}');
    expect(lines).toEqual(['{"jsonrpc":"2.0","id":4,"error":{"code":-32603,"message":"internal error"}}']);
    expect(s.log()).toContain('boom');
  });

  it('waits for in-flight requests before done resolves', async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    const bridge: McpBridge = {
      leaves: () => [{ path: ['wait'], description: 'Wait', properties: {} }],
      async invoke() {
        await gate;
        return { stdout: 'done\n', exitCode: 0 };
      },
    };
    const s = session(bridge);
    s.input.write('{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait"}}\n');
    s.input.end();
    let finished = false;
    void s.server.done.then(() => (finished = true));
    await new Promise((r) => setTimeout(r, 10));
    expect(finished).toBe(false);
    release();
    await s.server.done;
    expect(s.lines()).toHaveLength(1);
  });

  it('close() stops reading and resolves once idle', async () => {
    const s = session(legacyLockBridge());
    s.input.write('{"jsonrpc":"2.0","id":1,"method":"ping"}\n');
    await new Promise((r) => setTimeout(r, 5));
    await s.server.close();
    s.input.write('{"jsonrpc":"2.0","id":2,"method":"ping"}\n');
    await new Promise((r) => setTimeout(r, 5));
    expect(s.lines()).toEqual(['{"jsonrpc":"2.0","id":1,"result":{}}']);
  });
});
