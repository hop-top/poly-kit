/**
 * @module output/registry
 *
 * Registry holds Formatter implementations keyed by Formatter.key. Mirrors
 * Go's *Registry. Built-in formatters register against `defaultRegistry` at
 * module-load time.
 */

import type { Formatter } from './formatter';

/**
 * Registry holds Formatter implementations.
 *
 * - `register` throws on duplicate keys (init-time fail-loud).
 * - `override` quietly replaces — use when intentionally swapping a built-in.
 * - `lookup` returns the formatter for a key, or undefined.
 * - `keys` returns sorted format identifiers.
 * - `formatters` returns sorted formatters.
 * - `extensionMap` returns ext→key (e.g. ".csv" → "csv"); later
 *   registrations win on collision (alphabetical key order is the tie-break).
 */
export class Registry {
  private readonly byKey = new Map<string, Formatter>();

  /**
   * Adds f to the registry. Throws Error on duplicate key or empty key.
   * Use `override` to intentionally replace.
   */
  register<T>(f: Formatter<T>): void {
    if (f.key === '') {
      throw new Error('output: formatter key is empty');
    }
    if (this.byKey.has(f.key)) {
      throw new Error(
        `output: formatter "${f.key}" already registered (use override to replace)`,
      );
    }
    this.byKey.set(f.key, f as Formatter);
  }

  /** Replaces (or registers) the formatter for f.key. */
  override<T>(f: Formatter<T>): void {
    if (f.key === '') {
      throw new Error('output: formatter key is empty');
    }
    this.byKey.set(f.key, f as Formatter);
  }

  /** Returns the formatter registered under key, if any. */
  lookup(key: string): Formatter | undefined {
    return this.byKey.get(key);
  }

  /** Returns all registered format keys, sorted alphabetically. */
  keys(): readonly string[] {
    return [...this.byKey.keys()].sort();
  }

  /** Returns all registered formatters in key order. */
  formatters(): readonly Formatter[] {
    return this.keys().map(k => this.byKey.get(k) as Formatter);
  }

  /**
   * Returns extension→key mappings (e.g. ".csv" → "csv") across all
   * registered formatters. Later registrations win on collision; iteration
   * is in sorted-key order so resolution is deterministic.
   */
  extensionMap(): ReadonlyMap<string, string> {
    const out = new Map<string, string>();
    for (const k of this.keys()) {
      const f = this.byKey.get(k);
      if (!f) continue;
      for (const ext of f.extensions) {
        out.set(ext.toLowerCase(), k);
      }
    }
    return out;
  }
}

/** Module-level Registry. Built-ins register here at module-load time. */
export const defaultRegistry = new Registry();

/** Returns a new, empty Registry — no built-ins registered. */
export function newRegistry(): Registry {
  return new Registry();
}
