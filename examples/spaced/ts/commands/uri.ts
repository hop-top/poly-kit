/** URI command wiring for spaced. */

import { Command } from 'commander';
import {
  completeVanity,
  handler,
  parse,
  resolve,
  type HandlerSpec,
  type Policy,
} from '../../../../sdk/ts/src/uri';

const policy: Policy = {
  defaultNamespaceSegments: 2,
  schemeNamespaceSegments: { spaced: 2 },
  vanityAliases: [
    { from: 'spaced://ift-5', to: 'spaced://hop-top/spaced/IFT-5' },
    { from: 'spaced://starship', to: 'spaced://hop-top/spaced/IFT-5' },
    { from: 'spaced://starman', to: 'spaced://hop-top/spaced/Starman' },
  ],
  actionRoutes: {
    'mission.inspect': {
      command: 'spaced',
      args: ['mission', 'inspect', '{id}'],
    },
  },
};

const handlerSpec: HandlerSpec = {
  vendor: 'hop-top',
  app: 'spaced',
  language: 'ts',
  scheme: 'spaced',
  appPath: 'spaced',
  displayName: 'spaced',
};

export function uriCommand(): Command {
  const cmd = new Command('uri')
    .description('Inspect spaced custom URI scheme metadata');

  cmd
    .command('parse <uri>')
    .description('Parse a spaced:// URI')
    .option('--json', 'Print JSON')
    .action((input: string, opts: { json?: boolean }) => {
      const parsed = parse(input, policy);
      if (opts.json) {
        console.log(JSON.stringify(parsed, null, 2));
        return;
      }
      console.log(`scheme=${parsed.scheme} namespace=${parsed.namespace} id=${parsed.id} action=${parsed.action}`);
    });

  cmd
    .command('resolve <uri>')
    .description('Resolve a spaced:// URI action without executing it')
    .option('--json', 'Print JSON')
    .action((input: string, opts: { json?: boolean }) => {
      const plan = resolve(input, policy);
      if (opts.json) {
        console.log(JSON.stringify(plan, null, 2));
        return;
      }
      console.log([plan.command, ...plan.args].join(' '));
    });

  cmd
    .command('complete <input>')
    .description('Print vanity URI completion candidates')
    .action((input: string) => {
      for (const candidate of completeVanity(input, policy)) {
        console.log(`${candidate.from}\tcanonical: ${candidate.to}`);
      }
    });

  cmd
    .command('handler-id')
    .description('Print the spaced URI handler ID')
    .action(() => {
      console.log(handler.id(handlerSpec));
    });

  return cmd;
}
