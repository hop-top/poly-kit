/**
 * @module output/flags
 *
 * Commander integration: registerOutputFlags adds the standard output
 * flag suite (--format, --format-opt, --format-help, --cols, --template,
 * --output|-o) to a Commander program. Mirrors Go's RegisterFlagsWith.
 */

import type { Command } from 'commander';
import { Registry, defaultRegistry } from './registry';

/** Internal stash mapping a Commander program → Registry. */
const registryStash = new WeakMap<Command, Registry>();

/** Returns the Registry attached to cmd (walks parents) or defaultRegistry. */
export function registryFor(cmd: Command): Registry {
  for (let c: Command | null = cmd; c; c = (c as { parent?: Command | null }).parent ?? null) {
    const r = registryStash.get(c);
    if (r) return r;
  }
  return defaultRegistry;
}

/** Options for registerOutputFlags. */
export interface RegisterOutputFlagsOptions {
  /** Custom registry (default: defaultRegistry). */
  registry?: Registry;
  /** Per-flag opt-out. */
  disable?: {
    format?: boolean;
    formatOpt?: boolean;
    formatHelp?: boolean;
    cols?: boolean;
    template?: boolean;
    output?: boolean;
  };
}

/**
 * Adds the output flag suite to a Commander program. Each flag is opt-out
 * via `opts.disable.<flag>`. The active registry can be swapped via
 * `opts.registry`; tests + multi-CLI binaries use this for isolated sets.
 */
export function registerOutputFlags(
  program: Command,
  opts: RegisterOutputFlagsOptions = {},
): void {
  const registry = opts.registry ?? defaultRegistry;
  const d = opts.disable ?? {};

  if (registry !== defaultRegistry) {
    registryStash.set(program, registry);
  }

  if (!d.format) {
    program.option(
      '--format <fmt>',
      `Output format (${registry.keys().join(', ')})`,
      'table',
    );
  }
  // --format is the only output flag in the cross-language parity FLAGS
  // contract; everything below is kit plumbing hidden from default --help.
  // Adopters opting in via --help-all still see them (Commander's
  // showHidden flag is wired by applyHelpTheme).
  if (!d.formatOpt) {
    const opt = program
      .createOption(
        '--format-opt <kv...>',
        'Per-format option as key=value (repeatable; bool keys may omit =value)',
      )
      .default([] as string[])
      .argParser(collectKv);
    opt.hidden = true;
    program.addOption(opt);
  }
  if (!d.formatHelp) {
    const opt = program.createOption(
      '--format-help [fmt]',
      'Show available formats and their options (use --format-help <key> for one)',
    );
    opt.hidden = true;
    program.addOption(opt);
  }
  if (!d.cols) {
    const colsOpt = program
      .createOption('--cols <cols...>', 'Restrict columns to this comma-separated list (repeatable)')
      .default([] as string[])
      .argParser(collectCols);
    colsOpt.hidden = true;
    program.addOption(colsOpt);
    const columnsOpt = program
      .createOption('--columns <cols...>', 'Alias for --cols')
      .default([] as string[])
      .argParser(collectCols);
    columnsOpt.hidden = true;
    program.addOption(columnsOpt);
  }
  if (!d.template) {
    const opt = program.createOption(
      '--template <tpl>',
      'eta template applied to results (mutually exclusive with --cols)',
    );
    opt.hidden = true;
    program.addOption(opt);
  }
  if (!d.output) {
    const opt = program
      .createOption('-o, --output <path>', 'Write output to path (use - or empty for stdout)')
      .default('');
    opt.hidden = true;
    program.addOption(opt);
  }
}

/** Variadic collector for --format-opt. Each invocation appends one value. */
function collectKv(value: string, prev: readonly string[]): string[] {
  return [...prev, value];
}

/**
 * Variadic collector for --cols / --columns. Splits each invocation on
 * commas so callers can pass either repeated flags or a single
 * comma-separated string. Dedupe happens later in dispatch.
 */
function collectCols(value: string, prev: readonly string[]): string[] {
  return [...prev, ...value.split(',')];
}

/**
 * Merges --cols + --columns from a Commander opts object into a single
 * ordered, deduped string[]. Trims whitespace, drops empty entries.
 */
export function resolveCols(opts: Record<string, unknown>): string[] {
  const raw: string[] = [];
  for (const k of ['cols', 'columns']) {
    const v = opts[k];
    if (Array.isArray(v)) raw.push(...(v as string[]));
  }
  const seen = new Set<string>();
  const out: string[] = [];
  for (const item of raw) {
    for (const part of item.split(',')) {
      const p = part.trim();
      if (p === '' || seen.has(p)) continue;
      seen.add(p);
      out.push(p);
    }
  }
  return out;
}
