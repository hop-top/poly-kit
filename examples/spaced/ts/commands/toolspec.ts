/**
 * commands/toolspec.ts — load and validate spaced.toolspec.yaml
 */

import { Command } from 'commander';
import * as path from 'path';
import { loadToolSpec, validateToolSpec } from '../../../../sdk/ts/src/toolspec';

function countCommands(cmds: { children?: any[] }[]): number {
  let n = 0;
  for (const c of cmds) {
    n++;
    if (c.children) n += countCommands(c.children);
  }
  return n;
}

export function toolspecCommand(): Command {
  return new Command('toolspec')
    .description('Load and validate spaced.toolspec.yaml')
    .action(() => {
      const yamlPath = path.resolve(
        __dirname, '../../spaced.toolspec.yaml',
      );

      const spec = loadToolSpec(yamlPath);
      const errors = validateToolSpec(spec);

      console.log('');
      console.log(`  Name     : ${spec.name}`);
      console.log(`  Version  : ${spec.schemaVersion}`);
      console.log(`  Commands : ${countCommands(spec.commands)}`);

      if (errors.length > 0) {
        console.log(`  Errors   : ${errors.length}`);
        for (const e of errors) {
          console.log(`    - ${e}`);
        }
      } else {
        console.log('  Status   : valid');
      }
      console.log('');
    });
}
