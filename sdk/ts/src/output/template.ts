/**
 * @module output/template
 *
 * --template support via eta engine. EJS-style `<%= field %>` syntax.
 *
 * Template input: `{ items, cols, data }` where `items` is an array of
 * objects projected through the ColumnSpec list (or the raw rows when no
 * ColumnSpec is provided), `cols` is the list of header names, and `data`
 * is the original payload for advanced use.
 */

import { Eta } from 'eta';
import type { ColumnSpec } from './formatter';

const eta = new Eta({ autoEscape: false });

/**
 * Renders an eta template against `data`.
 *
 * @param out  Destination writable stream.
 * @param src  Template source string.
 * @param data Single row or readonly array.
 * @param columns Optional ColumnSpec list for header derivation + projection.
 */
export async function renderTemplate(
  out: NodeJS.WritableStream,
  src: string,
  data: unknown,
  columns?: readonly ColumnSpec[],
): Promise<void> {
  const rows = Array.isArray(data) ? (data as readonly unknown[]) : [data];
  const items = projectItems(rows, columns);
  const cols = columns
    ? columns.map(c => c.header)
    : items.length > 0 && typeof items[0] === 'object' && items[0] !== null
      ? Object.keys(items[0] as Record<string, unknown>)
      : [];
  const input = { items, cols, data };

  let rendered: string;
  try {
    rendered = String(eta.renderString(src, input as never));
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    throw new Error(`template error: ${msg}`);
  }
  out.write(rendered);
}

function projectItems(
  rows: readonly unknown[],
  columns?: readonly ColumnSpec[],
): readonly Record<string, unknown>[] {
  if (!columns) {
    return rows.map(r => (r === null || typeof r !== 'object' ? {} : (r as Record<string, unknown>)));
  }
  return rows.map(row => {
    const r = (row ?? {}) as Record<string, unknown>;
    const out: Record<string, unknown> = {};
    for (const c of columns) out[c.header] = r[c.key];
    return out;
  });
}
