/**
 * @module output/formatters/csv
 *
 * Built-in CSV formatter. Uses csv-stringify (sync API) for RFC 4180 quoting.
 * Honors `delimiter`, `no-header`, `quote-all`, `crlf` options + `cols`
 * filtering.
 */

import { stringify } from 'csv-stringify/sync';
import type { Formatter, Options } from '../formatter';

export const csvFormatter: Formatter = {
  key: 'csv',
  extensions: ['.csv'],
  options: [
    { name: 'delimiter', type: 'string', default: ',', usage: 'field delimiter' },
    { name: 'no-header', type: 'bool', default: false, usage: 'omit header row' },
    {
      name: 'quote-all',
      type: 'bool',
      default: false,
      usage: 'quote every field, not just those needing it',
    },
    {
      name: 'crlf',
      type: 'bool',
      default: false,
      usage: 'use CRLF line endings (default LF)',
    },
  ],
  render(out, data, opts: Options, cols) {
    const rows = normalise(data);
    if (rows.length === 0) return;

    const allHeaders = Object.keys(rows[0] ?? {});
    const headers =
      cols.length > 0 ? cols.filter(c => allHeaders.includes(c)) : allHeaders;
    if (headers.length === 0) return;

    const delimiter = (opts['delimiter'] as string) ?? ',';
    const noHeader = (opts['no-header'] as boolean) ?? false;
    const quoteAll = (opts['quote-all'] as boolean) ?? false;
    const crlf = (opts['crlf'] as boolean) ?? false;

    if (delimiter.length !== 1) {
      throw new Error(`option "delimiter": delimiter must be exactly one character`);
    }

    const records = rows.map(r => headers.map(h => stringify_cell(r[h])));
    const csvRows = noHeader ? records : [headers, ...records];

    const text = stringify(csvRows, {
      delimiter,
      record_delimiter: crlf ? 'windows' : 'unix',
      quoted: quoteAll,
      // No `header: true` — we already prepend headers ourselves to keep
      // row layout uniform with csv format and to make `no-header` toggle
      // a single decision point.
    });
    out.write(text);
  },
};

function normalise(v: unknown): Record<string, unknown>[] {
  if (Array.isArray(v)) return v as Record<string, unknown>[];
  return [v as Record<string, unknown>];
}

function stringify_cell(v: unknown): string {
  if (v === null || v === undefined) return '';
  return String(v);
}
