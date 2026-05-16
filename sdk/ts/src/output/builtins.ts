/**
 * @module output/builtins
 *
 * Registers built-in formatters against defaultRegistry at module-load.
 * Importing this module triggers registration as a side effect — same
 * init-time guarantee Go achieves via init() functions.
 */

import { defaultRegistry } from './registry';
import { jsonFormatter } from './formatters/json';
import { yamlFormatter } from './formatters/yaml';
import { tableFormatter } from './formatters/table';
import { csvFormatter } from './formatters/csv';
import { textFormatter } from './formatters/text';

let registered = false;

/**
 * Register the built-in formatters (json, yaml, table, csv, text) against
 * defaultRegistry. Idempotent — safe to call from multiple entry points.
 */
export function registerBuiltins(): void {
  if (registered) return;
  defaultRegistry.register(jsonFormatter);
  defaultRegistry.register(yamlFormatter);
  defaultRegistry.register(tableFormatter);
  defaultRegistry.register(csvFormatter);
  defaultRegistry.register(textFormatter);
  registered = true;
}

// Side-effect: register on import so adopters that pull anything from
// @hop-top/kit/output get the built-ins ready.
registerBuiltins();
