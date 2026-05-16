/**
 * @module output/formatters/table
 *
 * Built-in table formatter. Hand-rolled aligner (matches the original
 * output.ts behaviour). Honors `cols` to filter columns; preserves first-
 * row key order otherwise.
 */

import type { Formatter, Options } from '../formatter';

export const tableFormatter: Formatter = {
  key: 'table',
  extensions: [],
  options: [],
  render(out, data, _opts: Options, cols) {
    const rows = normalise(data);
    if (rows.length === 0) return;

    const allHeaders = Object.keys(rows[0] ?? {});
    const headers =
      cols.length > 0 ? cols.filter(c => allHeaders.includes(c)) : allHeaders;
    if (headers.length === 0) return;

    const cells = rows.map(row =>
      headers.map(h => String(row[h] ?? '')),
    );
    const widths = headers.map((h, ci) =>
      Math.max(h.length, ...cells.map(r => r[ci].length)),
    );
    const pad = (s: string, w: number) => s + ' '.repeat(w - s.length);
    const line = (parts: string[]) =>
      parts.map((c, i) => pad(c, widths[i])).join('  ');

    out.write(line(headers) + '\n');
    for (const row of cells) {
      out.write(line(row) + '\n');
    }
  },
};

function normalise(v: unknown): Record<string, unknown>[] {
  if (Array.isArray(v)) return v as Record<string, unknown>[];
  return [v as Record<string, unknown>];
}
