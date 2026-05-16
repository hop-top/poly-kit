/**
 * @module output/formatters/json
 *
 * Built-in JSON formatter. Honors `indent` option (default 2) + trailing
 * newline. When `cols` is non-empty, projects rows to header-keyed objects.
 */

import type { Formatter, Options } from '../formatter';

/**
 * Build the JSON formatter. The `columns` ref is optional; when provided
 * (via dispatch with a ColumnSpec list), rows are projected before encode.
 */
export const jsonFormatter: Formatter = {
  key: 'json',
  extensions: ['.json'],
  options: [
    { name: 'indent', type: 'int', default: 2, usage: 'spaces per indent level' },
  ],
  render(out, data, opts: Options, cols) {
    const indent = (opts['indent'] as number) ?? 2;
    const value = projectForJson(data, cols);
    out.write(JSON.stringify(value, null, indent) + '\n');
  },
};

function projectForJson(
  data: unknown,
  cols: readonly string[],
): unknown {
  if (cols.length === 0) return data;
  if (Array.isArray(data)) {
    return data.map(row => projectRow(row, cols));
  }
  return projectRow(data, cols);
}

function projectRow(row: unknown, cols: readonly string[]): unknown {
  if (row === null || typeof row !== 'object') return row;
  const r = row as Record<string, unknown>;
  const out: Record<string, unknown> = {};
  for (const c of cols) out[c] = r[c];
  return out;
}
