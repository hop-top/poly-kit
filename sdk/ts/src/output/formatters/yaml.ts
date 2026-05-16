/**
 * @module output/formatters/yaml
 *
 * Built-in YAML formatter. Honors `flow-level` option (default -1, mapping
 * to js-yaml's default block style). When `cols` is non-empty, projects
 * rows to header-keyed objects.
 */

import * as yaml from 'js-yaml';
import type { Formatter, Options } from '../formatter';

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
    const value = projectForYaml(data, cols);
    out.write(yaml.dump(value, { flowLevel }));
  },
};

function projectForYaml(
  data: unknown,
  cols: readonly string[],
): unknown {
  if (cols.length === 0) return data;
  if (Array.isArray(data)) {
    return data.map(row => projectRow(row, cols));
  }
  return projectRow(data, cols);
}

function projectRow(row: unknown, cols: readonly string[]): unknown {
  if (row === null || typeof row !== 'object') return row;
  const r = row as Record<string, unknown>;
  const out: Record<string, unknown> = {};
  for (const c of cols) out[c] = r[c];
  return out;
}
