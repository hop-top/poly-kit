/**
 * commands/abort.ts — abort <mission> --reason
 */

import { Command } from 'commander';
import { findMission } from '../data';

const ABORT_NOTES = [
  'Abort sequence nominal. Engines nominal. Anomaly nominal.',
  'Abort triggered. No comment until RCA complete (est. 6–18 months).',
  'Launch scrubbed. Weather blamed. Weather unavailable for rebuttal.',
  'Abort. Booster returned safely. Payload had opinions. Payload lost.',
  'T-0 hold. Propellant loading resumed. Then stopped. Then resumed. Stand by.',
];

export function abortCommand(): Command {
  return new Command('abort')
    .description('Abort a mission')
    .argument('<mission>', 'Mission name to abort')
    .requiredOption('--reason <text>', 'Reason for abort (will be logged, not believed)')
    .action(function (missionName: string) {
      const opts = this.opts();
      const mission = findMission(missionName);

      if (!mission) {
        process.stderr.write(`  error: unknown mission: "${missionName}"\n`);
        process.stderr.write(`  Run 'spaced mission list' to see available missions.\n`);
        process.exit(1);
      }

      const idx = Date.now() % ABORT_NOTES.length;
      const note = ABORT_NOTES[idx];

      console.log('');
      console.log(`  ✗ ABORT: ${mission.name}`);
      console.log(`  Reason logged: "${opts['reason'] as string}"`);
      console.log(`  Reason believed: [CLASSIFIED]`);
      console.log('');
      console.log(`  Status: ${note}`);
      console.log('');
      console.log('  Mission grounded. FAA notified. Press release drafted.');
      console.log('  Tweet scheduled for 3am. No further questions.');
      console.log('');
    });
}
