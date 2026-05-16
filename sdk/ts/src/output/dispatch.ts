/**
 * @module output/dispatch
 *
 * Resolves the active output flags from a Commander Command and renders
 * data, honoring --format, --format-opt, --cols/--columns, --template,
 * --format-help, and --output|-o per the rules wired by registerOutputFlags.
 *
 * Mirrors Go's output.Dispatch.
 */

import type { Command } from 'commander';
import { createWriteStream } from 'node:fs';
import { extname } from 'node:path';
import type { ColumnSpec, Formatter } from './formatter';
import { parseOptions } from './formatter';
import { registryFor, resolveCols } from './flags';
import { renderFormatHelp } from './format_help';
import { validateCols } from './projection';
import { renderTemplate } from './template';

/** Sentinel value of --output meaning "write to stdout". */
const STDOUT_SENTINEL = '-';

/** Optional inputs to dispatch. */
export interface DispatchOptions<T = unknown> {
  /** ColumnSpec list for projection + --cols validation. */
  columns?: readonly ColumnSpec[];
  /** Override registry (default: registry stashed on cmd, else defaultRegistry). */
  registry?: import('./registry').Registry;
  /** Default format when --format isn't explicit (default: "table"). */
  defaultFormat?: string;
  // Phantom T for caller type inference.
  _phantom?: T;
}

/**
 * Renders `data` through the active formatter resolved from cmd's flags.
 *
 * Resolution order:
 *   1. Resolve writer: empty/'-' → cmd.stdout (process.stdout), else file.
 *   2. --format-help short-circuit: list registry or show one formatter.
 *   3. Resolve format: explicit --format wins; else infer from --output ext.
 *   4. Mismatch detection: explicit --format + ext mapping to different
 *      formatter is a hard error.
 *   5. --template: build {items, cols} input, run eta, return.
 *   6. Else: parseOptions, validateCols, formatter.render.
 */
export async function dispatch<T>(
  cmd: Command,
  data: T | readonly T[],
  options: DispatchOptions<T> = {},
): Promise<void> {
  const opts = cmd.optsWithGlobals
    ? cmd.optsWithGlobals()
    : (cmd as { opts(): Record<string, unknown> }).opts();
  const registry = options.registry ?? registryFor(cmd);
  const defaultFormat = options.defaultFormat ?? 'table';

  // 1. Writer.
  const path =
    typeof opts['output'] === 'string' ? (opts['output'] as string) : '';
  const { writer, close } = await resolveWriter(path);

  try {
    // 2. --format-help short-circuit.
    const fhVal = opts['formatHelp'];
    if (fhVal !== undefined && fhVal !== false) {
      // commander stores [fmt] as: `true` when bare, string when value supplied.
      const explicitKey = typeof fhVal === 'string' ? fhVal : '';
      const formatChanged = isFormatExplicit(cmd);
      const key = explicitKey || (formatChanged ? String(opts['format'] ?? '') : '');
      renderFormatHelp(writer, registry, key);
      return;
    }

    // 3 + 4. Format resolution.
    const format = resolveFormat(cmd, opts, registry, path, defaultFormat);

    // 5. Template escape hatch.
    const tmplSrc =
      typeof opts['template'] === 'string' ? (opts['template'] as string) : '';
    const cols = resolveCols(opts);
    if (tmplSrc) {
      if (cols.length > 0) {
        throw new Error('--template and --cols are mutually exclusive');
      }
      await renderTemplate(writer, tmplSrc, data, options.columns);
      return;
    }

    // 6. Formatter render.
    const formatter = registry.lookup(format);
    if (!formatter) {
      throw new Error(
        `unknown output format "${format}" (valid: ${registry.keys().join(', ')})`,
      );
    }
    const formatOpt = (opts['formatOpt'] as string[]) ?? [];
    const parsedOpts = parseOptions(formatOpt, formatter.options);
    if (cols.length > 0) {
      const rows = Array.isArray(data) ? (data as readonly unknown[]) : [data];
      validateCols(rows, options.columns, cols);
    }
    await Promise.resolve(
      (formatter as Formatter).render(writer, data as never, parsedOpts, cols),
    );
  } finally {
    if (close) await close();
  }
}

interface ResolvedWriter {
  writer: NodeJS.WritableStream;
  close: (() => Promise<void>) | null;
}

async function resolveWriter(path: string): Promise<ResolvedWriter> {
  if (!path || path === STDOUT_SENTINEL) {
    return { writer: process.stdout, close: null };
  }
  const stream = createWriteStream(path, { flags: 'w', mode: 0o644 });
  // Catch open errors (ENOENT for missing dir, EACCES, etc).
  await new Promise<void>((resolve, reject) => {
    const onErr = (err: Error) => {
      stream.removeListener('open', onOpen);
      reject(new Error(`open output "${path}": ${err.message}`));
    };
    const onOpen = () => {
      stream.removeListener('error', onErr);
      resolve();
    };
    stream.once('error', onErr);
    stream.once('open', onOpen);
  });
  return {
    writer: stream,
    close: () =>
      new Promise<void>((resolve, reject) => {
        stream.end((err?: Error | null) => (err ? reject(err) : resolve()));
      }),
  };
}

/** Returns true if --format was explicitly set on the command line. */
function isFormatExplicit(cmd: Command): boolean {
  // Commander's `getOptionValueSourceWithGlobals` reports the source.
  type WithSource = {
    getOptionValueSourceWithGlobals?(name: string): string | undefined;
    getOptionValueSource?(name: string): string | undefined;
  };
  const c = cmd as unknown as WithSource;
  const src =
    c.getOptionValueSourceWithGlobals?.('format') ??
    c.getOptionValueSource?.('format');
  return src === 'cli';
}

/** Resolves the active format key honoring explicit > extension > default. */
function resolveFormat(
  cmd: Command,
  opts: Record<string, unknown>,
  registry: import('./registry').Registry,
  path: string,
  defaultFormat: string,
): string {
  const format = (opts['format'] as string | undefined) || defaultFormat;
  const explicit = isFormatExplicit(cmd);

  if (!path || path === STDOUT_SENTINEL) return format;

  const ext = extname(path).toLowerCase();
  if (!ext) return format;

  const mapped = registry.extensionMap().get(ext);
  if (!mapped) return format;

  if (!explicit) return mapped;
  if (mapped !== format) {
    const primary = primaryExt(registry, format);
    throw new Error(
      `format "${format}" does not match output extension "${ext}" ` +
        `(use -o file${primary} or --format ${mapped})`,
    );
  }
  return format;
}

function primaryExt(
  registry: import('./registry').Registry,
  key: string,
): string {
  const f = registry.lookup(key);
  if (!f) return `.${key}`;
  return f.extensions[0] ?? `.${key}`;
}
