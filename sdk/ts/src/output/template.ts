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
import { deriveHeaders, projectRows } from './projection';

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
  // Templates always index items by field, so non-object rows become {}.
  const items = projectRows(
    rows.map(r => (r === null || typeof r !== 'object' ? {} : r)),
    columns,
    [],
  );
  const cols = deriveHeaders(rows, columns);
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
