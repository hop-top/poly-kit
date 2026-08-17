/**
 * @module output/projection
 *
 * Helpers for projecting structured data through ColumnSpec lists. Mirrors
 * Go's projection.go (filterColumns / projectToMaps / TableHeaders).
 *
 * Unlike Go (which uses `table:""` struct tags), TS callers pass an explicit
 * ColumnSpec[] list. When no columns are provided, callers fall back to
 * deriving headers from the first row's own enumerable keys.
 *
 * A ColumnSpec's `header` and `key` are the same name: validation and value
 * lookup are one operation, matching what Go's `table:""` tag can express.
 * `--cols` reorders as well as selects, so the user's order wins over the
 * ColumnSpec list's.
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
 * Selects columns named by `selected`, in the order `selected` gives them.
 * `--cols` reorders as well as selects, so the user's sequence wins over the
 * ColumnSpec list's. Unknown names throw with the available header list.
 */
export function filterColumns(
  columns: readonly ColumnSpec[],
  selected: readonly string[],
): readonly ColumnSpec[] {
  const byName = new Map(columns.map(c => [c.header, c]));
  return selected.map(name => {
    const c = byName.get(name);
    if (!c) {
      const valid = columns.map(x => x.header).join(', ');
      throw new Error(`unknown column "${name}" (valid: ${valid})`);
    }
    return c;
  });
}

/**
 * Resolves the ordered column names for a render: the ColumnSpec list (or
 * first-row keys) narrowed and reordered by `cols` when the user supplied it.
 *
 * Because header == key, the result is directly usable both as the output
 * labels and as the lookup names on each row.
 */
export function resolveColumnNames(
  rows: readonly unknown[],
  columns: readonly ColumnSpec[] | undefined,
  cols: readonly string[],
): readonly string[] {
  if (cols.length === 0) return deriveHeaders(rows, columns);
  if (columns && columns.length > 0) {
    return filterColumns(columns, cols).map(c => c.header);
  }
  // No ColumnSpec — `cols` names keys on the row directly, in user order.
  const available = deriveHeaders(rows);
  if (available.length === 0) return cols;
  const have = new Set(available);
  return cols.filter(c => have.has(c));
}

/**
 * Projects rows to plain objects keyed by column name, in resolved order.
 * With neither a ColumnSpec list nor `cols`, rows pass through untouched so
 * JSON/YAML keep whatever shape the caller handed in.
 */
export function projectRows(
  rows: readonly unknown[],
  columns: readonly ColumnSpec[] | undefined,
  cols: readonly string[],
): readonly Record<string, unknown>[] {
  const passthrough = cols.length === 0 && !(columns && columns.length > 0);
  const names = passthrough ? [] : resolveColumnNames(rows, columns, cols);

  return rows.map(row => {
    const r = (row ?? {}) as Record<string, unknown>;
    if (passthrough) return r;
    const out: Record<string, unknown> = {};
    for (const name of names) out[name] = r[name];
    return out;
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
