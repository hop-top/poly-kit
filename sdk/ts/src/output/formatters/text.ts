/**
 * @module output/formatters/text
 *
 * Built-in text formatter. Three styles: kv, lines, paragraph.
 *
 * - kv:        "key{sep}value" lines, blank line between records
 * - lines:     tab-separated, no header, one row per line
 * - paragraph: "Record N:" header line then "  field: value" lines
 *
 * Zero deps. Mirrors Go text formatter byte-for-byte.
 */

import type { Formatter, Options } from '../formatter';

const STYLE_KV = 'kv';
const STYLE_LINES = 'lines';
const STYLE_PARAGRAPH = 'paragraph';

export const textFormatter: Formatter = {
  key: 'text',
  extensions: ['.txt'],
  options: [
    {
      name: 'style',
      type: 'enum',
      default: STYLE_KV,
      enum: [STYLE_KV, STYLE_LINES, STYLE_PARAGRAPH],
      usage: 'output style',
    },
    {
      name: 'separator',
      type: 'string',
      default: '=',
      usage: 'kv separator (kv style only)',
    },
  ],
  render(out, data, opts: Options, cols) {
    const rows = normalise(data);
    if (rows.length === 0) return;

    const allHeaders = Object.keys(rows[0] ?? {});
    const headers =
      cols.length > 0 ? cols.filter(c => allHeaders.includes(c)) : allHeaders;
    if (headers.length === 0) return;

    const style = ((opts['style'] as string) || STYLE_KV) as
      | 'kv'
      | 'lines'
      | 'paragraph';
    const sep = (opts['separator'] as string) || '=';

    switch (style) {
      case STYLE_KV:
        return renderKV(out, rows, headers, sep);
      case STYLE_LINES:
        return renderLines(out, rows, headers);
      case STYLE_PARAGRAPH:
        return renderParagraph(out, rows, headers);
    }
  },
};

function normalise(v: unknown): Record<string, unknown>[] {
  if (Array.isArray(v)) return v as Record<string, unknown>[];
  return [v as Record<string, unknown>];
}

function fmt(v: unknown): string {
  if (v === null || v === undefined) return '';
  return String(v);
}

function renderKV(
  out: NodeJS.WritableStream,
  rows: readonly Record<string, unknown>[],
  headers: readonly string[],
  sep: string,
): void {
  rows.forEach((r, i) => {
    if (i > 0) out.write('\n');
    for (const h of headers) {
      out.write(`${h}${sep}${fmt(r[h])}\n`);
    }
  });
}

function renderLines(
  out: NodeJS.WritableStream,
  rows: readonly Record<string, unknown>[],
  headers: readonly string[],
): void {
  for (const r of rows) {
    out.write(headers.map(h => fmt(r[h])).join('\t') + '\n');
  }
}

function renderParagraph(
  out: NodeJS.WritableStream,
  rows: readonly Record<string, unknown>[],
  headers: readonly string[],
): void {
  rows.forEach((r, i) => {
    if (i > 0) out.write('\n');
    out.write(`Record ${i + 1}:\n`);
    for (const h of headers) {
      out.write(`  ${h}: ${fmt(r[h])}\n`);
    }
  });
}
