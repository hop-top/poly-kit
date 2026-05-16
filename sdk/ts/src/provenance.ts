/**
 * @module provenance
 * @package @hop-top/kit
 *
 * Factor 11 — Provenance.
 *
 * Attaches origin metadata to any data payload for JSON output,
 * enabling consumers to verify source, timing, and method.
 */

export interface Provenance {
  source: string;
  /** ISO 8601 timestamp */
  timestamp: string;
  method: string;
}

/**
 * Wraps `data` with provenance metadata under `_meta`.
 */
export function withProvenance<T>(
  data: T,
  p: Provenance,
): { data: T; _meta: Provenance } {
  return { data, _meta: p };
}
