/**
 * @module output/formatters/yaml
 *
 * Built-in YAML formatter. Honors `flow-level` option (default -1, mapping
 * to js-yaml's default block style). Emitted key order follows the ColumnSpec
 * list, narrowed and reordered by `cols`; without either, the payload passes
 * through untouched.
 */

import * as yaml from 'js-yaml';
import type { Formatter, Options } from '../formatter';
import { projectForEncoding } from './project';

export const yamlFormatter: Formatter = {
  key: 'yaml',
  extensions: ['.yaml', '.yml'],
  options: [
    {
      name: 'flow-level',
      type: 'int',
      default: -1,
      usage: 'level at which to switch from block to flow style',
    },
  ],
  render(out, data, opts: Options, cols) {
    const flowLevel = (opts['flow-level'] as number) ?? -1;
    const value = projectForEncoding(data, cols);
    out.write(yaml.dump(value, { flowLevel }));
  },
};
