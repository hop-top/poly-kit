/**
 * commands/alias.ts — alias management (list, add, remove)
 */

import { Command } from 'commander';
import * as path from 'path';
import { configDir } from '../../../../sdk/ts/src/xdg';
import { AliasStore } from '../../../../sdk/ts/src/alias';

function storePath(): string {
  return path.join(configDir('spaced'), 'config.yaml');
}

export function aliasCommand(): Command {
  const cmd = new Command('alias')
    .description('Manage command aliases');

  cmd
    .command('list')
    .description('List all aliases')
    .action(() => {
      const store = new AliasStore(storePath());
      store.load();
      const all = store.all();
      const names = Object.keys(all).sort();
      if (names.length === 0) {
        console.log('  No aliases configured.');
        return;
      }
      console.log('');
      for (const name of names) {
        console.log(`  ${name} → ${all[name]}`);
      }
      console.log('');
    });

  cmd
    .command('add <name> <target>')
    .description('Create an alias: name expands to target')
    .action((name: string, target: string) => {
      const store = new AliasStore(storePath());
      store.load();
      store.set(name, target);
      store.save();
      console.log(`  alias ${name} → ${target}`);
    });

  cmd
    .command('remove <name>')
    .description('Remove an alias')
    .action((name: string) => {
      const store = new AliasStore(storePath());
      store.load();
      if (!store.get(name)) {
        console.error(`  alias "${name}" not found`);
        process.exitCode = 1;
        return;
      }
      store.remove(name);
      store.save();
      console.log(`  removed alias "${name}"`);
    });

  return cmd;
}
