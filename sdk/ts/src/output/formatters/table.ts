/**
 * @module output/formatters/table
 *
 * Built-in table formatter. Hand-rolled aligner (matches the original
 * output.ts behaviour). Column order comes from the ColumnSpec list, or the
 * first row's key order when none was supplied; `cols` narrows and reorders.
 */

import type { Formatter, Options } from '../formatter';
import { deriveHeaders } from '../projection';

export const tableFormatter: Formatter = {
  key: 'table',
  extensions: [],
  options: [],
  render(out, data, _opts: Options, cols) {
    const rows = normalise(data);
    // Emptiness is a ROW-count decision: no rows means no output at all,
    // not a bare header line.
    if (rows.length === 0) return;

    // `cols` arrives pre-resolved from dispatch; empty means payload keys.
    const headers = cols.length > 0 ? cols : deriveHeaders(rows);
    if (headers.length === 0) return;

    const cells = rows.map(row =>
      headers.map(h => String(row[h] ?? '')),
    );
    const widths = headers.map((h, ci) =>
      Math.max(h.length, ...cells.map(r => r[ci].length)),
    );
    const pad = (s: string, w: number) => s + ' '.repeat(w - s.length);
    const line = (parts: readonly string[]) =>
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
