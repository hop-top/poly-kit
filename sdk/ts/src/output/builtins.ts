/**
 * @module output/builtins
 *
 * Registers built-in formatters against defaultRegistry at module-load.
 * Importing this module triggers registration as a side effect — same
 * init-time guarantee Go achieves via init() functions.
 */

import { defaultRegistry } from './registry';
import { csvFormatter } from './formatters/csv';
import { jsonFormatter } from './formatters/json';
import { tableFormatter } from './formatters/table';
import { textFormatter } from './formatters/text';
import { yamlFormatter } from './formatters/yaml';

let registered = false;

/**
 * Register the built-in formatters (csv, json, table, text, yaml) against
 * defaultRegistry. Idempotent — safe to call from multiple entry points.
 */
export function registerBuiltins(): void {
  if (registered) return;
  defaultRegistry.register(csvFormatter);
  defaultRegistry.register(jsonFormatter);
  defaultRegistry.register(tableFormatter);
  defaultRegistry.register(textFormatter);
  defaultRegistry.register(yamlFormatter);
  registered = true;
}

// Side-effect: register on import so adopters that pull anything from
// @hop-top/kit/output get the built-ins ready.
registerBuiltins();
