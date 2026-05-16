/**
 * commands/daemon.ts — daemon list / status <id> / stop <id> / stop --all
 */

import { Command } from 'commander';
import { Bus, createEvent } from '../../../../sdk/ts/src/bus';
import { DAEMONS, findDaemon } from '../data';

function statusIcon(status: string): string {
  switch (status) {
    case 'RUNNING':  return '●';
    case 'RESOLVED': return '○';
    default:         return '?';
  }
}

export function daemonCommand(bus?: Bus): Command {
  const daemon = new Command('daemon')
    .description('Manage background controversy processes');

  // --- daemon list ---
  daemon
    .command('list')
    .description('List all known controversy daemons')
    .action(function () {
      const COL = { id: 32, started: 14, status: 12 };

      const header =
        '  ' +
        'DAEMON ID'.padEnd(COL.id) +
        'STARTED'.padEnd(COL.started) +
        'STATUS';
      const sep = '  ' + '─'.repeat(COL.id + COL.started + COL.status + 4);

      console.log('');
      console.log(header);
      console.log(sep);

      for (const d of DAEMONS) {
        const row =
          '  ' +
          d.id.padEnd(COL.id) +
          d.started.padEnd(COL.started) +
          statusIcon(d.status) + ' ' + d.status;
        console.log(row);
      }

      const running  = DAEMONS.filter(d => d.status === 'RUNNING').length;
      const resolved = DAEMONS.filter(d => d.status === 'RESOLVED').length;

      console.log('');
      console.log(`  Running: ${running}   Resolved: ${resolved}   Total: ${DAEMONS.length}`);
      console.log('');
      console.log('  ● RUNNING  = Active, ongoing, legally significant');
      console.log('  ○ RESOLVED = Settled, concluded, archived (not forgotten)');
      console.log('');
    });

  // --- daemon status <id> ---
  daemon
    .command('status <id>')
    .description('Get detailed status of a daemon by ID')
    .action(function (id: string) {
      const d = findDaemon(id);

      if (!d) {
        process.stderr.write(`  error: unknown daemon: "${id}"\n`);
        process.stderr.write(`  Run 'spaced daemon list' to see available daemons.\n`);
        process.exit(1);
      }

      console.log('');
      console.log(`  DAEMON: ${d.id}`);
      console.log(`  ${'─'.repeat(50)}`);
      console.log(`  Title:   ${d.title}`);
      console.log(`  Status:  ${statusIcon(d.status)} ${d.status}`);
      console.log(`  Started: ${d.started}`);
      console.log('');
      console.log('  Description:');
      // Word-wrap at 70 chars
      const words = d.description.split(' ');
      let line = '  ';
      for (const word of words) {
        if (line.length + word.length + 1 > 72) {
          console.log(line);
          line = '  ' + word;
        } else {
          line += (line === '  ' ? '' : ' ') + word;
        }
      }
      if (line.trim()) console.log(line);

      console.log('');
      console.log('  References:');
      for (const ref of d.refs) {
        console.log(`    [${ref.outlet}] ${ref.headline}`);
        console.log(`           ${ref.url}`);
      }
      console.log('');
    });

  // --- daemon stop <id> / daemon stop --all ---
  daemon
    .command('stop [id]')
    .description('Attempt to stop a daemon (results may vary)')
    .option('--all', 'Stop all daemons simultaneously', false)
    .action(function (id: string | undefined) {
      const opts = this.opts();
      const stopAll = opts['all'] as boolean;

      if (stopAll) {
        const running = DAEMONS.filter(d => d.status === 'RUNNING');
        const newDaemon = {
          id: 'musk-response-to-this-cli',
          status: 'RUNNING',
        };

        console.log('');
        console.log(`  ✗ STOP FAILED: all daemons (${running.length}/${running.length})`);
        console.log(`  Stopped: 0`);
        console.log(`  Still running: ${running.length}`);
        console.log(`  New daemons spawned during stop attempt: 1`);
        console.log(`    → ${newDaemon.id}  [RUNNING since just now]`);
        console.log('');
        console.log('  The daemons are self-perpetuating. This is a known issue.');
        console.log('  Recommended action: document them. Stop filing for injunctions.');
        console.log('');
        return;
      }

      if (!id) {
        process.stderr.write('  error: provide a daemon ID or use --all\n');
        process.stderr.write('  Run \'spaced daemon list\' to see available daemons.\n');
        process.exit(1);
      }

      const d = findDaemon(id);

      if (!d) {
        process.stderr.write(`  error: unknown daemon: "${id}"\n`);
        process.stderr.write(`  Run 'spaced daemon list' to see available daemons.\n`);
        process.exit(1);
      }

      bus?.publish(createEvent(
        'kit.spaced.daemon.stopped', 'spaced',
        { daemon: id },
      ));

      if (d.status === 'RESOLVED') {
        console.log('');
        console.log(`  ○ DAEMON ALREADY RESOLVED: ${d.id}`);
        console.log(`  Resolved on: (see settlement terms, FOIA #TBD)`);
        console.log('  Nothing further to stop. The record remains.');
        console.log('');
        return;
      }

      console.log('');
      console.log(`  ✗ STOP FAILED: ${d.id}`);
      console.log(`  The daemon persists. Regulatory agencies, journalists,`);
      console.log(`  and approximately 400 million Twitter users keep it alive.`);
      console.log('');
      console.log(`  Suggested alternatives:`);
      console.log(`    → Settle out of court`);
      console.log(`    → Issue apology (optional, per legal)`);
      console.log(`    → Wait for news cycle to shift (avg: 3 days)`);
      console.log(`    → Acquire the outlet reporting it (expensive, impractical)`);
      console.log('');
    });

  return daemon;
}
