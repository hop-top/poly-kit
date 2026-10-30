/**
 * @module mcp/stdio
 * @package @hop-top/kit
 *
 * The stdio binding for the dual-spec MCP surface: newline-delimited
 * JSON-RPC over a pair of byte streams (by default the process's
 * stdin/stdout), driving the same era dispatch the HTTP handler uses.
 *
 * Wire rules (stdio binding, 2024-11-05 and 2026-07-28):
 *   - one JSON-RPC message per line; output lines carry no embedded
 *     newline, and the output stream carries nothing but protocol
 *     messages — diagnostics go to the log stream (stderr);
 *   - a notification (no `id` member) is never answered;
 *   - each message is a single request: a JSON array (a batch) is
 *     refused with -32600 and `id: null`;
 *   - a line that is not JSON is answered -32700 with `id: null`;
 *   - requests run concurrently, so replies may arrive out of order —
 *     each carries its request's id.
 *
 * stdio has no header layer: era detection uses the body markers only
 * (`params._meta` protocolVersion, `server/discover`), and the
 * 2026-07-28 header checks (V4, V6, V7) do not apply. Gates that read
 * headers see none — see the MCP guide for what that means for
 * auth-required and confirmation-gated tools.
 */

import { createInterface } from 'node:readline';
import type { Readable, Writable } from 'node:stream';

import {
  dispatcherFactoryOf,
  McpDispatcher,
  parseJsonRpcRequest,
  type JsonRpcRequest,
  type McpHandler,
  type McpHttpRequest,
  type McpHttpResponse,
} from './dispatch.js';
import { errorMessage, goJSONParseMessage } from './legacy.js';
import {
  encodeEnvelope,
  MCP_ERR_INTERNAL,
  MCP_ERR_INVALID_REQUEST,
  MCP_ERR_PARSE,
  RawJSON,
  resolveMcpConfig,
  type JsonRpcErrorObject,
  type McpBridge,
  type McpMountOptions,
} from './types.js';

/** Options for {@link serveStdio}. */
export interface McpStdioOptions {
  /** Where requests are read from. Default: `process.stdin`. */
  input?: Readable;
  /** Where replies are written. Default: `process.stdout`. */
  output?: Writable;
  /** Where diagnostics are written. Default: `process.stderr`. */
  log?: Writable;
  /**
   * Mount options, when serving a bridge. A handler from
   * createMcpHandler already carries its own; passing both throws.
   */
  mount?: McpMountOptions;
}

/** A running stdio session. */
export interface McpStdioServer {
  /**
   * Resolves once input has ended (or {@link close} was called) and
   * every in-flight request has been answered.
   */
  readonly done: Promise<void>;
  /** Stops reading input; resolves like {@link done}. */
  close(): Promise<void>;
}

type Serve = (req: McpHttpRequest, rpc: JsonRpcRequest) => Promise<McpHttpResponse>;

/**
 * Serves MCP over stdio for a bridge, or for a handler built by
 * createMcpHandler (its bridge and mount options are reused).
 *
 * A handler built some other way is called with an HTTP-shaped POST
 * per message; that serves the 2024-11-05 era, but such a handler
 * cannot know the request came over stdio, so 2026-07-28 requests fail
 * its header checks. Pass the bridge or a createMcpHandler handler to
 * serve both eras.
 *
 * ```ts
 * import { serveStdio } from '@hop-top/kit/mcp';
 *
 * const server = serveStdio(bridge);
 * await server.done; // the host closed stdin
 * ```
 */
export function serveStdio(
  target: McpBridge | McpHandler,
  opts: McpStdioOptions = {},
): McpStdioServer {
  const serve = resolveServe(target, opts.mount);
  const input = opts.input ?? process.stdin;
  const output = opts.output ?? process.stdout;
  const log = opts.log ?? process.stderr;

  const inFlight = new Set<Promise<void>>();
  const write = (line: string): void => {
    output.write(line.endsWith('\n') ? line : `${line}\n`);
  };
  const warn = (msg: string): void => {
    log.write(`mcp stdio: ${msg}\n`);
  };

  const rl = createInterface({ input, crlfDelay: Infinity, terminal: false });
  rl.on('line', (line) => {
    const task = handleLine(line, serve, write, warn);
    if (task === undefined) return;
    inFlight.add(task);
    void task.finally(() => inFlight.delete(task));
  });
  // A broken stream ends the session instead of crashing the process.
  const onInputError = (err: unknown): void => {
    warn(`input error: ${errorMessage(err)}`);
    rl.close();
  };
  const onOutputError = (err: unknown): void => {
    warn(`output error: ${errorMessage(err)}`);
    rl.close();
  };
  input.on('error', onInputError);
  output.on('error', onOutputError);

  const done = new Promise<void>((resolve) => {
    rl.once('close', () => {
      void drain(inFlight).then(() => {
        input.off('error', onInputError);
        output.off('error', onOutputError);
        resolve();
      });
    });
  });

  return {
    done,
    close() {
      rl.close();
      return done;
    },
  };
}

/** Picks the dispatch function for a bridge or a handler. */
function resolveServe(
  target: McpBridge | McpHandler,
  mount: McpMountOptions | undefined,
): Serve {
  if (typeof target === 'function') {
    if (mount !== undefined) {
      throw new TypeError(
        'serveStdio: mount options apply to a bridge; a handler carries its own',
      );
    }
    const factory = dispatcherFactoryOf(target);
    if (factory !== undefined) {
      const dispatcher = factory('stdio');
      return (req, rpc) => dispatcher.dispatch(req, rpc);
    }
    return (req) => target(req);
  }
  const dispatcher = new McpDispatcher(target, resolveMcpConfig(mount ?? {}), 'stdio');
  return (req, rpc) => dispatcher.dispatch(req, rpc);
}

/** Waits until no request is in flight, including ones added meanwhile. */
async function drain(inFlight: Set<Promise<void>>): Promise<void> {
  while (inFlight.size > 0) {
    await Promise.allSettled([...inFlight]);
  }
}

/**
 * Frames, parses and dispatches one input line. Returns the pending
 * work for a dispatched message, or undefined when the line was
 * answered (or dropped) synchronously.
 */
function handleLine(
  line: string,
  serve: Serve,
  write: (line: string) => void,
  warn: (msg: string) => void,
): Promise<void> | undefined {
  const text = line.trim();
  if (text === '') return undefined;

  let decoded: unknown;
  try {
    decoded = JSON.parse(text);
  } catch {
    write(errorLine(null, { code: MCP_ERR_PARSE, message: `parse error: ${goJSONParseMessage(text)}` }));
    return undefined;
  }
  if (Array.isArray(decoded)) {
    write(errorLine(null, {
      code: MCP_ERR_INVALID_REQUEST,
      message: 'invalid request: batch requests are not supported',
    }));
    return undefined;
  }
  if (decoded === null || typeof decoded !== 'object') {
    write(errorLine(null, {
      code: MCP_ERR_INVALID_REQUEST,
      message: 'invalid request: not a JSON-RPC object',
    }));
    return undefined;
  }

  const obj = decoded as Record<string, unknown>;
  const rpc = parseJsonRpcRequest(text);
  const isNotification = rpc.id === undefined;

  if (typeof obj.method !== 'string') {
    // A response (result / error, no method): this server sends no
    // requests, so there is nothing to correlate it with.
    if ('result' in obj || 'error' in obj) {
      warn('ignored a JSON-RPC response from the client');
      return undefined;
    }
    if (isNotification) return undefined;
    write(errorLine(rpc.id, {
      code: MCP_ERR_INVALID_REQUEST,
      message: 'invalid request: missing method',
    }));
    return undefined;
  }

  const req: McpHttpRequest = { method: 'POST', body: text };
  return serve(req, rpc).then(
    (res) => {
      // A notification is never answered, whatever the handler said.
      if (isNotification || res.body === '') return;
      write(res.body);
    },
    (err: unknown) => {
      warn(`${rpc.method}: ${errorMessage(err)}`);
      if (isNotification) return;
      write(errorLine(rpc.id, { code: MCP_ERR_INTERNAL, message: 'internal error' }));
    },
  );
}

/** One JSON-RPC error line; `null` renders `"id":null`. */
function errorLine(id: RawJSON | null | undefined, error: JsonRpcErrorObject): string {
  return encodeEnvelope({ id: id === null ? new RawJSON('null') : id, error });
}
