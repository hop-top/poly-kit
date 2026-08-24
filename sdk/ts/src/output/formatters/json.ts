/**
 * @module output/formatters/json
 *
 * Built-in JSON formatter. Honors `indent` option (default 2) + trailing
 * newline. Emitted key order follows the ColumnSpec list, narrowed and
 * reordered by `cols`; without either, the payload passes through untouched.
 */

import type { Formatter, Options } from '../formatter';
import { projectForEncoding } from './project';

export const jsonFormatter: Formatter = {
  key: 'json',
  extensions: ['.json'],
  options: [
    { name: 'indent', type: 'int', default: 2, usage: 'spaces per indent level' },
  ],
  render(out, data, opts: Options, cols) {
    const indent = (opts['indent'] as number) ?? 2;
    const value = projectForEncoding(data, cols);
    out.write(JSON.stringify(value, null, indent) + '\n');
  },
};
