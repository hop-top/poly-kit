/**
 * @module output/format_help
 *
 * --format-help rendering. Lists registered formatters or shows one
 * formatter's options. Reuses the table formatter for the help table itself.
 */

import type { OptionSpec } from './formatter';
import { Registry } from './registry';

/** Per-format row in the catalog table. */
export interface FormatSummary {
  format: string;
  extensions: string;
  options: string;
}

/** Per-option row in the format-help options table. */
export interface OptionRow {
  name: string;
  type: string;
  default: string;
  enum: string;
  usage: string;
}

/** Returns one FormatSummary per registered formatter, sorted by key. */
export function listFormats(registry: Registry): FormatSummary[] {
  return registry.formatters().map(f => ({
    format: f.key,
    extensions: f.extensions.join(', '),
    options: [...f.options.map(o => o.name)].sort().join(', '),
  }));
}

/** Returns one OptionRow per OptionSpec on the formatter under key. */
export function formatOptions(registry: Registry, key: string): OptionRow[] {
  const f = registry.lookup(key);
  if (!f) {
    throw new Error(
      `unknown format "${key}" (valid: ${registry.keys().join(', ')})`,
    );
  }
  return f.options.map(o => optionRow(o));
}

function optionRow(o: OptionSpec): OptionRow {
  return {
    name: o.name,
    type: o.type,
    default: o.default !== undefined ? String(o.default) : '',
    enum: o.enum ? o.enum.join(', ') : '',
    usage: o.usage,
  };
}

/**
 * Writes --format-help output. Empty `format` lists all formatters; a
 * specific key prints that formatter's options.
 */
export function renderFormatHelp(
  out: NodeJS.WritableStream,
  registry: Registry,
  format: string,
): void {
  const tableFormatter = registry.lookup('table');
  if (!tableFormatter) {
    throw new Error('output: table formatter required for --format-help');
  }
  if (!format) {
    tableFormatter.render(out, listFormats(registry), {}, []);
    return;
  }
  const rows = formatOptions(registry, format);
  if (rows.length === 0) {
    out.write(`format "${format}" has no options\n`);
    return;
  }
  tableFormatter.render(out, rows, {}, []);
}
