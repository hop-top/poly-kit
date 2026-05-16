/**
 * commands/mission.ts — mission list / inspect / search
 */

import { Command } from 'commander';
import { MISSIONS, findMission, pick } from '../data';

function getFormat(cmd: Command): string {
  // Walk up to root to find --format option
  let c: Command | null = cmd;
  while (c) {
    const opts = c.opts();
    if (opts['format']) return opts['format'] as string;
    c = c.parent;
  }
  return 'table';
}

function outcomeIcon(outcome: string): string {
  switch (outcome) {
    case 'SUCCESS': return '✓';
    case 'RUD':     return '✗';
    case 'PARTIAL': return '~';
    default:        return '?';
  }
}

export function missionCommand(): Command {
  const mission = new Command('mission')
    .description('Query mission history');

  // --- mission list ---
  mission
    .command('list')
    .description('List all recorded missions')
    .action(function () {
      const format = getFormat(this);

      if (format === 'json') {
        console.log(JSON.stringify(MISSIONS, null, 2));
        return;
      }

      if (format === 'yaml') {
        // Minimal YAML serialiser (no dep)
        const lines: string[] = ['missions:'];
        for (const m of MISSIONS) {
          lines.push(`  - name: ${m.name}`);
          lines.push(`    vehicle: ${m.vehicle}`);
          lines.push(`    date: ${m.date}`);
          lines.push(`    outcome: ${m.outcome}`);
          lines.push(`    market_mood: ${pick(m.market_mood)}`);
        }
        console.log(lines.join('\n'));
        return;
      }

      // Table (default)
      const COL = {
        name:    20,
        vehicle: 16,
        date:    12,
        outcome: 10,
        mood:    20,
      };

      const header =
        '  ' +
        'MISSION'.padEnd(COL.name) +
        'VEHICLE'.padEnd(COL.vehicle) +
        'DATE'.padEnd(COL.date) +
        'OUTCOME'.padEnd(COL.outcome) +
        'MARKET MOOD';

      const sep = '  ' + '─'.repeat(
        COL.name + COL.vehicle + COL.date + COL.outcome + COL.mood,
      );

      console.log('');
      console.log(header);
      console.log(sep);

      for (const m of MISSIONS) {
        const row =
          '  ' +
          m.name.padEnd(COL.name) +
          m.vehicle.padEnd(COL.vehicle) +
          m.date.padEnd(COL.date) +
          (outcomeIcon(m.outcome) + ' ' + m.outcome).padEnd(COL.outcome) +
          pick(m.market_mood);
        console.log(row);
      }

      console.log('');
      console.log('  * RUD = Rapid Unscheduled Disassembly  (company terminology, not ours)');
      console.log('');
    });

  // --- mission inspect <name> ---
  mission
    .command('inspect <name>')
    .description('Inspect a mission by name')
    .action(function (name: string) {
      const m = findMission(name);
      if (!m) {
        process.stderr.write(`  error: mission not found: "${name}"\n`);
        process.stderr.write(`  Run 'spaced mission list' to see available missions.\n`);
        process.exit(1);
      }

      console.log('');
      console.log(`  MISSION: ${m.name}`);
      console.log(`  Vehicle: ${m.vehicle}`);
      console.log(`  Date:    ${m.date}`);
      console.log(`  Outcome: ${outcomeIcon(m.outcome)} ${m.outcome}`);
      console.log(`  Mood:    ${pick(m.market_mood)}`);
      console.log('');
      console.log('  Notes:');
      for (const note of m.notes) {
        console.log(`    → ${note}`);
      }
      console.log('');
    });

  // --- mission search --query ---
  mission
    .command('search')
    .description('Search missions by keyword')
    .requiredOption('--query <text>', 'Search term')
    .action(function () {
      const opts = this.opts();
      const query = (opts['query'] as string).toLowerCase();

      const results = MISSIONS.filter(m =>
        m.name.toLowerCase().includes(query) ||
        m.vehicle.toLowerCase().includes(query) ||
        m.outcome.toLowerCase().includes(query) ||
        m.notes.some(n => n.toLowerCase().includes(query)),
      );

      if (results.length === 0) {
        console.log(`  No missions matched "${opts['query']}".`);
        console.log('  (The missions happened though. We have footage.)');
        return;
      }

      console.log('');
      console.log(`  Found ${results.length} mission(s) matching "${opts['query']}":`);
      console.log('');
      for (const m of results) {
        console.log(`  ${outcomeIcon(m.outcome)} ${m.name}  [${m.vehicle} · ${m.date}]`);
      }
      console.log('');
    });

  return mission;
}
