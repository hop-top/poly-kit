/**
 * Structured logger wrapping pino — applies kit's charmtone theme
 * colors to stderr output.
 *
 * API: `logger.info('msg', 'key', val, 'key2', val2)` (variadic key-value)
 * This matches Go's charm/log API, not pino's native object API.
 * Pino handles transports, serialization, and performance under the hood.
 */

import pino from 'pino';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface Logger {
  info(msg: string, ...keyvals: any[]): void;
  warn(msg: string, ...keyvals: any[]): void;
  error(msg: string, ...keyvals: any[]): void;
  debug(msg: string, ...keyvals: any[]): void;
  trace(msg: string, ...keyvals: any[]): void;
}

export interface LoggerOptions {
  quiet?: boolean;
  noColor?: boolean;
}

// ---------------------------------------------------------------------------
// Theme — charmtone palette (matches Go kit/log)
// ---------------------------------------------------------------------------

const CHERRY = [0xed, 0x4a, 0x5e] as const;
const YAM    = [0xe5, 0xa1, 0x4e] as const;
const SQUID  = [0x85, 0x81, 0x83] as const;
const SMOKE  = [0xbf, 0xbc, 0xc8] as const;

type RGB = readonly [number, number, number];

function fg(rgb: RGB, text: string): string {
  return `\x1b[38;2;${rgb[0]};${rgb[1]};${rgb[2]}m${text}\x1b[0m`;
}

function bold(text: string): string {
  return `\x1b[1m${text}\x1b[22m`;
}

// ---------------------------------------------------------------------------
// Kit-themed pino transport
// ---------------------------------------------------------------------------

const LEVEL_STYLES: Record<number, { label: string; color: RGB; bold: boolean }> = {
  10: { label: 'TRAC', color: SMOKE,  bold: false },
  20: { label: 'DEBU', color: SMOKE,  bold: false },
  30: { label: 'INFO', color: SQUID,  bold: false },
  40: { label: 'WARN', color: YAM,    bold: true },
  50: { label: 'ERRO', color: CHERRY, bold: true },
  60: { label: 'ERRO', color: CHERRY, bold: true },
};

function kitTransport(noColor: boolean): pino.DestinationStream {
  return {
    write(chunk: string): void {
      try {
        const obj = JSON.parse(chunk);
        const style = LEVEL_STYLES[obj.level] ?? LEVEL_STYLES[30];
        let prefix = noColor ? style.label : fg(style.color, style.label);
        if (style.bold && !noColor) prefix = bold(prefix);

        const msg = obj.msg ?? '';
        const kvParts: string[] = [];
        for (const [k, v] of Object.entries(obj)) {
          if (['level', 'time', 'pid', 'hostname', 'msg', 'v'].includes(k)) continue;
          const sv = String(v);
          kvParts.push(sv.includes(' ') ? `${k}="${sv}"` : `${k}=${sv}`);
        }
        const kv = kvParts.length ? ' ' + kvParts.join(' ') : '';
        process.stderr.write(`${prefix} ${msg}${kv}\n`);
      } catch {
        process.stderr.write(chunk);
      }
    },
  };
}

// ---------------------------------------------------------------------------
// Variadic key-value → pino object adapter
// ---------------------------------------------------------------------------

function kvToObj(keyvals: any[]): Record<string, any> {
  const obj: Record<string, any> = {};
  for (let i = 0; i < keyvals.length; i += 2) {
    const key = String(keyvals[i]);
    obj[key] = i + 1 < keyvals.length ? keyvals[i + 1] : '';
  }
  return obj;
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function createLogger(opts?: LoggerOptions): Logger {
  const quiet = opts?.quiet ?? false;
  const noColor = opts?.noColor ?? false;

  const p = pino({
    level: quiet ? 'warn' : 'debug',
  }, kitTransport(noColor));

  return {
    info:  (msg, ...kv) => p.info(kvToObj(kv), msg),
    warn:  (msg, ...kv) => p.warn(kvToObj(kv), msg),
    error: (msg, ...kv) => p.error(kvToObj(kv), msg),
    debug: (msg, ...kv) => p.debug(kvToObj(kv), msg),
    trace: (msg, ...kv) => p.trace(kvToObj(kv), msg),
  };
}

// ---------------------------------------------------------------------------
// Verbose-aware factory
// ---------------------------------------------------------------------------

/**
 * Map verbose count to pino level: 0=info, 1=debug, 2+=trace.
 * Quiet overrides to warn regardless of verbose count.
 */
export function withVerbose(
  opts: LoggerOptions & { verbose?: number },
): Logger {
  const quiet = opts.quiet ?? false;
  const noColor = opts.noColor ?? false;
  const v = opts.verbose ?? 0;

  let level: string;
  if (quiet) {
    level = 'warn';
  } else if (v >= 2) {
    level = 'trace';
  } else if (v >= 1) {
    level = 'debug';
  } else {
    level = 'info';
  }

  const p = pino({ level }, kitTransport(noColor));

  return {
    info:  (msg, ...kv) => p.info(kvToObj(kv), msg),
    warn:  (msg, ...kv) => p.warn(kvToObj(kv), msg),
    error: (msg, ...kv) => p.error(kvToObj(kv), msg),
    debug: (msg, ...kv) => p.debug(kvToObj(kv), msg),
    trace: (msg, ...kv) => p.trace(kvToObj(kv), msg),
  };
}
