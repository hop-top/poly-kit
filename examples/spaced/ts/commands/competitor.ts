/**
 * commands/competitor.ts — competitor compare <name> --metric
 */

import { Command } from 'commander';
import { findCompetitor } from '../data';

export function competitorCommand(): Command {
  const competitor = new Command('competitor')
    .description('Compare SpaceX against its competitors');

  competitor
    .command('compare <name>')
    .description('Compare a competitor to SpaceX')
    .option('--metric <value>', 'Comma-separated metrics to show (default: all)', '')
    .action(function (name: string) {
      const opts = this.opts();
      const c = findCompetitor(name);

      if (!c) {
        process.stderr.write(`  error: unknown competitor: "${name}"\n`);
        const known = ['Boeing', 'Blue Origin', 'Virgin Galactic', 'ULA', 'Roscosmos'];
        process.stderr.write(`  Known competitors: ${known.join(', ')}\n`);
        process.exit(1);
      }

      const filterMetrics = opts['metric']
        ? (opts['metric'] as string).split(',').map((m: string) => m.trim().toLowerCase()).filter(Boolean)
        : [];

      console.log('');
      console.log(`  COMPETITOR: ${c.name}`);
      console.log(`  ${'─'.repeat(60)}`);
      console.log('');
      console.log(`  Founded:              ${c.founded}`);
      console.log(`  Active vehicles:      ${c.rockets.join(', ')}`);
      console.log(`  Crewed flights:       ${c.crewed_flights}`);
      console.log(`  Success rate:         ${c.launch_success_rate}`);
      console.log('');
      console.log(`  Notable achievement:  ${c.notable_achievement}`);
      console.log(`  Notable failure:      ${c.notable_failure}`);
      console.log('');
      console.log(`  Elon's opinion:       ${c.elon_opinion}`);
      console.log('');

      const metricKeys = filterMetrics.length > 0
        ? Object.keys(c.metrics).filter(k =>
          filterMetrics.some(f => k.toLowerCase().includes(f)),
        )
        : Object.keys(c.metrics);

      if (metricKeys.length > 0) {
        console.log('  Key metrics:');
        for (const key of metricKeys) {
          const label = key.replace(/_/g, ' ').padEnd(28);
          console.log(`    ${label} ${c.metrics[key]}`);
        }
        console.log('');
      }

      console.log('  vs SpaceX:');
      console.log('    SpaceX Falcon 9:    $2,600/kg to LEO (reuse amortized)');
      console.log('    SpaceX Starship:    ~$100/kg to LEO (target; unverified)');
      console.log('    SpaceX crewed:      14 Dragon crew flights; all successful');
      console.log('');
      console.log('  Disclaimer: This comparison is satirical. SpaceX may also have');
      console.log('  failures we have chosen not to highlight out of narrative convenience.');
      console.log('');
    });

  return competitor;
}
