/**
 * @module output
 * @package @hop-top/kit
 *
 * Output rendering — writes structured values to a writable stream in one of
 * the registered formats: table, JSON, YAML, CSV, text, etc.
 *
 * ## Quick start
 *
 * ```ts
 * import { render, TABLE_FORMAT, JSON_FORMAT, YAML_FORMAT } from '@hop-top/kit/output';
 *
 * // Write a table to stdout
 * render(process.stdout, TABLE_FORMAT, [{ id: '1', name: 'Alice' }]);
 *
 * // Write indented JSON
 * render(process.stdout, JSON_FORMAT, { ok: true });
 *
 * // Write YAML
 * render(process.stdout, YAML_FORMAT, { version: 1 });
 * ```
 *
 * ## Programmatic Formatter API
 *
 * For typed flag wiring + custom formatters use the Formatter / Registry
 * surface from `output/formatter` and `output/registry`. The `render()`
 * helper exported here is a thin shim over `defaultRegistry`.
 *
 * ## Backward compatibility
 *
 * `render(w, format, v)` keeps its original signature. New per-format
 * options + column projection are accessed via `dispatch()`.
 */

// Re-export the typed surface.
export type { Formatter, OptionSpec, OptionType, Options, ColumnSpec } from './output/formatter';
export { parseOptions, optionTypeName } from './output/formatter';
export { Registry, defaultRegistry, newRegistry } from './output/registry';
export { jsonFormatter } from './output/formatters/json';
export { yamlFormatter } from './output/formatters/yaml';
export { tableFormatter } from './output/formatters/table';

// Side-effect: register built-ins.
import './output/builtins';

import { defaultRegistry } from './output/registry';

/**
 * Supported output formats. Includes the original three for backward-compat
 * + new built-ins (csv, text) registered on the default registry.
 */
export type Format = string;

/** Constant for the JSON output format. */
export const JSON_FORMAT = 'json' as const;

/** Constant for the YAML output format. */
export const YAML_FORMAT = 'yaml' as const;

/** Constant for the table output format. */
export const TABLE_FORMAT = 'table' as const;

/** Constant for the CSV output format. */
export const CSV_FORMAT = 'csv' as const;

/** Constant for the text output format. */
export const TEXT_FORMAT = 'text' as const;

/**
 * Renders `v` to `w` in the requested `format`.
 *
 * Backward-compatible thin shim over `defaultRegistry.lookup(format).render`.
 *
 * @throws {Error} For unknown format strings.
 */
export function render(
  w: NodeJS.WritableStream,
  format: Format,
  v: unknown,
): void {
  const f = defaultRegistry.lookup(format);
  if (!f) {
    const valid = defaultRegistry.keys().join(', ');
    throw new Error(`unknown output format "${format}" (valid: ${valid})`);
  }
  // Built-ins json/yaml/table are sync; other registered formatters may
  // return a promise. Caller of the legacy render() expects sync, so we
  // do not await — sync formatters complete before this returns.
  void f.render(w, v as never, {}, []);
}
