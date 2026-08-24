/**
 * @module output/projection
 *
 * Helpers for projecting structured data through ColumnSpec lists. Mirrors
 * Go's projection.go (projectToMaps / TableHeaders).
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

import { columnName, type ColumnSpec } from './formatter';

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
 * Resolves the effective column list a formatter should render.
 *
 * Precedence, per the settled ordering contract:
 *   - `cols` non-empty  → the user's `--cols`, verbatim and in user order
 *     (rule 2: --cols reorders as well as selects).
 *   - else a ColumnSpec list → its headers, in list order (rule 1).
 *   - else                → empty, meaning "fall back to payload key order".
 *
 * Because header == key (rule 3), an ordered array of names carries
 * everything a formatter needs: the labels and the row lookup keys are the
 * same strings. Resolving here keeps precedence in one place and leaves the
 * public Formatter signature untouched.
 */
export function resolveEffectiveCols(
  cols: readonly string[],
  columns: readonly ColumnSpec[] | undefined,
): readonly string[] {
  if (cols.length > 0) return cols;
  if (columns && columns.length > 0) return columns.map(columnName);
  return [];
}

/**
 * Projects rows to plain objects keyed by column name, in `cols` order.
 * An empty `cols` passes rows through untouched, so JSON/YAML keep whatever
 * shape the caller handed in.
 */
export function projectRows(
  rows: readonly unknown[],
  cols: readonly string[],
): readonly Record<string, unknown>[] {
  return rows.map(row => {
    const r = (row ?? {}) as Record<string, unknown>;
    if (cols.length === 0) return r;
    const out: Record<string, unknown> = {};
    for (const name of cols) out[name] = r[name];
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
