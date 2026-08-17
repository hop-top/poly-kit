/**
 * @module output/formatters/project
 *
 * Shared projection shim for the encoding formatters (json, yaml). Unlike
 * table/csv/text, these two must preserve the payload's outer shape — a
 * single object stays an object, an array stays an array — and must leave
 * non-object payloads (scalars, arrays of scalars) untouched.
 */

import type { ColumnSpec } from '../formatter';
import { projectRows } from '../projection';

/**
 * Projects `data` for encoding, keeping its outer shape.
 *
 * Column order follows the ColumnSpec list, narrowed and reordered by `cols`.
 * With neither, `data` is returned as-is.
 */
export function projectForEncoding(
  data: unknown,
  cols: readonly string[],
  columns?: readonly ColumnSpec[],
): unknown {
  if (cols.length === 0 && !(columns && columns.length > 0)) return data;

  if (Array.isArray(data)) {
    const rows = data as readonly unknown[];
    // Non-object entries have no columns to project; pass the payload through
    // rather than flattening every element to an empty object.
    if (rows.some(r => r === null || typeof r !== 'object')) return data;
    return projectRows(rows, columns, cols);
  }

  if (data === null || typeof data !== 'object') return data;
  return projectRows([data], columns, cols)[0];
}
