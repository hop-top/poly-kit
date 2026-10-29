/**
 * Structured logger wrapping pino — applies kit's charmtone theme
 * colors to stderr output (or an injected `output` sink).
 *
 * API: `logger.info('msg', 'key', val, 'key2', val2)` (variadic key-value)
 * This matches Go's charm/log API, not pino's native object API.
 * Pino handles transports, serialization, and performance under the hood.
 *
 * The `-V` count → level mapping and the `--quiet` override both come from
 * the parity contract (`parity.json` verbosity block), so the level
 * vocabulary stays identical across the Go, TypeScript and Python ports.
 */

import pino from 'pino';

import { parity, type ParityData } from './tui/parity.js';

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

/** Minimal sink the logger writes rendered lines to. */
export interface LogOutput {
  write(chunk: string): unknown;
}

/** Kit level vocabulary (same spellings as Go/py and `parity.json`). */
export type LogLevelName = 'trace' | 'debug' | 'info' | 'warn' | 'error' | 'fatal';

export interface LoggerOptions {
  quiet?: boolean;
  noColor?: boolean;
  /**
   * Explicit threshold; wins over the factory default and the `-V` count.
   * `'silent'` drops everything. `quiet` only raises it (to the contract's
   * `quiet_override`), never lowers it — same as Go's `WithLevel`.
   * TS-only option; not a parity verbosity entry.
   */
  level?: LogLevelName | 'silent';
  /**
   * Where rendered lines go. Defaults to `process.stderr`, resolved at
   * write time. Mirrors Go's `(*log.Logger).SetOutput`; useful for
   * capturing output in tests.
   */
  output?: LogOutput;
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

function kitTransport(noColor: boolean, output?: LogOutput): pino.DestinationStream {
  // Resolve the default lazily so callers that swap process.stderr after
  // construction keep seeing their writes.
  const out = (): LogOutput => output ?? process.stderr;
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
        out().write(`${prefix} ${msg}${kv}\n`);
      } catch {
        out().write(chunk);
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
// Parity contract → pino level names
// ---------------------------------------------------------------------------

/** Pino level names the contract's level vocabulary may resolve to. */
const PINO_LEVELS = new Set(['trace', 'debug', 'info', 'warn', 'error', 'fatal']);

/**
 * Resolve a contract level name to a pino level name.
 *
 * The names are the cross-language vocabulary declared in `parity.json`;
 * pino happens to use the same spellings, so this is a membership check
 * rather than a translation table (Go needs one because charm/log has no
 * `trace`).
 */
function levelByName(name: string): string | undefined {
  return PINO_LEVELS.has(name) ? name : undefined;
}

/**
 * Resolve a `-V` count against a parity contract's `verbosity.levels`
 * table. Counts above the highest declared key clamp to that key's level;
 * an empty or unresolvable table falls back to `info`.
 *
 * Taking the contract as a parameter (rather than reading the module-level
 * `parity`) keeps the mapping testable against a constructed `ParityData`
 * without mutating the shared `parity.json`.
 */
export function verbosityLevel(d: ParityData, verbose: number): string {
  const levels = d.verbosity?.levels ?? {};
  // Highest declared count at or below `verbose` wins.
  let best = -1;
  for (const key of Object.keys(levels)) {
    const n = Number(key);
    if (!Number.isInteger(n)) continue;
    if (n <= verbose && n > best) best = n;
  }
  if (best < 0) return 'info';
  return levelByName(levels[String(best)]) ?? 'info';
}

/**
 * Resolve a parity contract's `verbosity.quiet_override` to a pino level
 * name. An unrecognized or absent override falls back to `warn`.
 */
export function quietLevel(d: ParityData): string {
  return levelByName(d.verbosity?.quiet_override ?? '') ?? 'warn';
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

const LEVEL_RANK: Record<string, number> = {
  trace: 10, debug: 20, info: 30, warn: 40, error: 50, fatal: 60, silent: Infinity,
};

/** Apply `quiet` to a threshold: raise to the quiet override, never lower. */
function applyQuiet(level: string, quiet: boolean): string {
  if (!quiet) return level;
  const q = quietLevel(parity);
  return LEVEL_RANK[level] < LEVEL_RANK[q] ? q : level;
}

function build(opts: LoggerOptions, defaultLevel: string): Logger {
  const level = applyQuiet(opts.level ?? defaultLevel, opts.quiet ?? false);
  const p = pino({ level }, kitTransport(opts.noColor ?? false, opts.output));

  return {
    info:  (msg, ...kv) => p.info(kvToObj(kv), msg),
    warn:  (msg, ...kv) => p.warn(kvToObj(kv), msg),
    error: (msg, ...kv) => p.error(kvToObj(kv), msg),
    debug: (msg, ...kv) => p.debug(kvToObj(kv), msg),
    trace: (msg, ...kv) => p.trace(kvToObj(kv), msg),
  };
}

export function createLogger(opts?: LoggerOptions): Logger {
  // Note: the default level here is `debug`, NOT the contract's
  // zero-verbosity level. createLogger predates the verbosity contract and
  // is deliberately chattier than `withVerbose({ verbose: 0 })`; only the
  // quiet override is contract-declared, so only it is wired.
  return build(opts ?? {}, 'debug');
}

// ---------------------------------------------------------------------------
// Verbose-aware factory
// ---------------------------------------------------------------------------

/**
 * Map verbose count to a pino level through the parity contract's
 * `verbosity.levels` table. An explicit `level` wins over the count.
 * Quiet raises to the contract's `quiet_override` level.
 */
export function withVerbose(
  opts: LoggerOptions & { verbose?: number },
): Logger {
  return build(opts, verbosityLevel(parity, opts.verbose ?? 0));
}
