/**
 * @module output/formatters/project
 *
 * Shared projection shim for the encoding formatters (json, yaml). Unlike
 * table/csv/text, these two must preserve the payload's outer shape — a
 * single object stays an object, an array stays an array — and must leave
 * non-object payloads (scalars, arrays of scalars) untouched.
 */

import { projectRows } from '../projection';

/**
 * Projects `data` for encoding, keeping its outer shape.
 *
 * `cols` arrives pre-resolved from dispatch and fixes the emitted key order;
 * empty means pass `data` through as-is.
 */
export function projectForEncoding(
  data: unknown,
  cols: readonly string[],
): unknown {
  if (cols.length === 0) return data;

  if (Array.isArray(data)) {
    const rows = data as readonly unknown[];
    // Non-object entries have no columns to project; pass the payload through
    // rather than flattening every element to an empty object.
    if (rows.some(r => r === null || typeof r !== 'object')) return data;
    return projectRows(rows, cols);
  }

  if (data === null || typeof data !== 'object') return data;
  return projectRows([data], cols)[0];
}
