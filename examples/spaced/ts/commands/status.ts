/** Root status command for spaced. */

import { Command } from 'commander';

function globalFormat(cmd: Command): string {
  let cur: Command | null = cmd;
  while (cur) {
    const opts = cur.opts();
    if (typeof opts['format'] === 'string' && opts['format']) return opts['format'];
    cur = cur.parent ?? null;
  }
  return 'table';
}

function spacedEnv(): string[] {
  return Object.keys(process.env)
    .filter((key) => key.startsWith('SPACED_'))
    .sort();
}

export function statusCommand(): Command {
  return new Command('status')
    .description('Show spaced runtime status')
    .action(function statusAction(this: Command) {
      const report = {
        name: 'spaced',
        version: '0.1.0',
        runtime: 'node',
        status: 'ok',
        env: spacedEnv(),
      };

      if (globalFormat(this) === 'json') {
        console.log(JSON.stringify(report, null, 2));
        return;
      }

      console.log('');
      console.log('  -- SPACED STATUS ----------------------------------');
      console.log(`  Name    : ${report.name}`);
      console.log(`  Version : ${report.version}`);
      console.log(`  Runtime : ${report.runtime}`);
      console.log(`  Status  : ${report.status}`);
      console.log(`  Env     : ${report.env.length ? report.env.join(', ') : '-'}`);
      console.log('');
    });
}
