/**
 * @module output/projection
 *
 * Helpers for projecting structured data through ColumnSpec lists. Mirrors
 * Go's projection.go (filterColumns / projectToMaps / TableHeaders).
 *
 * Unlike Go (which uses `table:""` struct tags), TS callers pass an explicit
 * ColumnSpec[] list. When no columns are provided, callers fall back to
 * deriving headers from the first row's own enumerable keys.
 */

import type { ColumnSpec } from './formatter';

/** Normalises data into a readonly array of plain objects (rows). */
export function normaliseRows<T>(data: T | readonly T[]): readonly T[] {
  return Array.isArray(data) ? (data as readonly T[]) : [data as T];
}

/** Returns the list of headers for a row source given an optional ColumnSpec list. */
export function deriveHeaders(
  rows: readonly unknown[],
  columns?: readonly ColumnSpec[],
): readonly string[] {
  if (columns && columns.length > 0) {
    return columns.map(c => c.header);
  }
  if (rows.length === 0) return [];
  const first = rows[0];
  if (first === null || typeof first !== 'object') return [];
  return Object.keys(first as Record<string, unknown>);
}

/**
 * Filters a ColumnSpec list to those whose `header` appears in `selected`.
 * Order is preserved from `columns` (caller's intended order). Unknown
 * names in `selected` throw with the available header list.
 */
export function filterColumns(
  columns: readonly ColumnSpec[],
  selected: readonly string[],
): readonly ColumnSpec[] {
  const have = new Set(columns.map(c => c.header));
  for (const name of selected) {
    if (!have.has(name)) {
      const valid = columns.map(c => c.header).join(', ');
      throw new Error(`unknown column "${name}" (valid: ${valid})`);
    }
  }
  const want = new Set(selected);
  return columns.filter(c => want.has(c.header));
}

/**
 * Builds a header→key lookup. Used to project rows for json/yaml/csv/text.
 * When no ColumnSpec list is provided, headers map to themselves (the row
 * already keys by the same names).
 */
export function buildHeaderToKey(
  columns?: readonly ColumnSpec[],
): Map<string, string> {
  const out = new Map<string, string>();
  if (!columns) return out;
  for (const c of columns) {
    out.set(c.header, c.key);
  }
  return out;
}

/**
 * Projects rows to plain objects keyed by header, optionally filtered by
 * `cols`. When `columns` is undefined, rows are returned as-is for JSON/
 * YAML pass-through. When `cols` is empty AND `columns` is defined,
 * every column is included.
 */
export function projectRows(
  rows: readonly unknown[],
  columns: readonly ColumnSpec[] | undefined,
  cols: readonly string[],
): readonly Record<string, unknown>[] {
  const active = columns
    ? cols.length > 0
      ? filterColumns(columns, cols)
      : columns
    : undefined;

  return rows.map(row => {
    const r = (row ?? {}) as Record<string, unknown>;
    if (active) {
      const out: Record<string, unknown> = {};
      for (const c of active) {
        out[c.header] = r[c.key];
      }
      return out;
    }
    if (cols.length > 0) {
      // No ColumnSpec provided — treat cols as direct keys on the row.
      const out: Record<string, unknown> = {};
      for (const k of cols) out[k] = r[k];
      return out;
    }
    return r;
  });
}

/**
 * Validates `cols` against either a ColumnSpec list (header set) or, when
 * absent, the keys of the first row. Returns silently when no headers can
 * be derived (caller's data has no schema we can check).
 */
export function validateCols(
  rows: readonly unknown[],
  columns: readonly ColumnSpec[] | undefined,
  cols: readonly string[],
): void {
  if (cols.length === 0) return;
  const headers = columns
    ? columns.map(c => c.header)
    : deriveHeaders(rows);
  if (headers.length === 0) return;
  const have = new Set(headers);
  for (const c of cols) {
    if (!have.has(c)) {
      throw new Error(`unknown column "${c}" (valid: ${headers.join(', ')})`);
    }
  }
}
