/**
 * @module output/formatters/csv
 *
 * Built-in CSV formatter. Honors `delimiter`, `no-header`, `quote-all`,
 * `crlf` options + `cols` filtering.
 *
 * Encoding is hand-rolled rather than delegated to csv-stringify. In its
 * `windows` record-delimiter mode that library left a field containing an
 * embedded LF UNQUOTED, so a single record decoded as two (or three)
 * records — structurally invalid CSV, not merely a quoting difference. It
 * also left a leading space bare where the other runtimes quote it.
 *
 * The rule: quote a field iff it contains the active delimiter, a double
 * quote, LF, or CR, or begins with a unicode whitespace character. Trailing
 * whitespace is not quoted. Internal quotes are doubled per RFC 4180.
 *
 * A quoted field's bytes are preserved verbatim in both line-ending modes and
 * both quoting paths. RFC 4180's `escaped` production lists CR and LF as
 * separate alternatives, so a bare CR between quotes is legal, and the W3C
 * CSV on the Web note states that line endings within escaped cells are not
 * normalised. The `crlf` option changes the record terminator and nothing
 * else.
 */

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
    // Headers are prepended here rather than via a library `header` option to
    // keep row layout uniform and make `no-header` a single decision point.
    const csvRows = noHeader ? records : [headers, ...records];

    const eol = crlf ? '\r\n' : '\n';
    let text = '';
    for (const cells of csvRows) {
      text += cells.map(c => encodeField(c, delimiter, quoteAll)).join(delimiter) + eol;
    }
    out.write(text);
  },
};

/**
 * Quote a field iff it contains the delimiter, a quote, LF or CR, or begins
 * with a unicode whitespace character. Note the asymmetry: a LEADING space
 * forces quoting, a trailing one does not. `\.` alone on a line terminates a
 * PostgreSQL COPY stream and is quoted defensively.
 */
function needsQuotes(field: string, delim: string): boolean {
  if (field === '') return false;
  if (field === '\\.') return true;
  if (field.includes(delim) || field.includes('"') || field.includes('\n') || field.includes('\r')) {
    return true;
  }
  return /^\s/u.test(field);
}

function encodeField(field: string, delim: string, quoteAll: boolean): string {
  if (!quoteAll && !needsQuotes(field, delim)) return field;
  // RFC 4180: an embedded quote is doubled. Everything else, CR and LF
  // included, is written through untouched.
  return `"${field.replace(/"/g, '""')}"`;
}

function normalise(v: unknown): Record<string, unknown>[] {
  if (Array.isArray(v)) return v as Record<string, unknown>[];
  return [v as Record<string, unknown>];
}

function stringify_cell(v: unknown): string {
  if (v === null || v === undefined) return '';
  return String(v);
}
