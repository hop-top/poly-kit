/**
 * commands/countdown.ts — countdown <mission>
 */

import { Command } from 'commander';
import { findMission } from '../data';

const HOLDS = [
  'T-10: Unexpected hold. LOX loading paused. LOX unavailable for comment.',
  'T-9: Range safety officer stepped away. Vending machine incident.',
  'T-7: FLTS check nominal. Everything else: TBD.',
  'T-4: Propellant loading complete. Ground crew celebrating early. Asked to stop.',
  'T-3: Launch director polled. All stations GO. One station VERBOSE. Noted.',
  'T-2: Engine chill complete. Merlin 1Ds at temp. Launch commit criteria: 29/30.',
  'T-1: RUD prediction model: inconclusive. Proceeding anyway.',
  'T-0: Ignition sequence start. Autonomous flight termination system armed.',
];

export function countdownCommand(): Command {
  return new Command('countdown')
    .description('Show countdown status for a mission')
    .argument('<mission>', 'Mission name')
    .action(function (missionName: string) {
      const mission = findMission(missionName);

      if (!mission) {
        process.stderr.write(`  error: unknown mission: "${missionName}"\n`);
        process.stderr.write(`  Run 'spaced mission list' to see available missions.\n`);
        process.exit(1);
      }

      console.log('');
      console.log(`  LAUNCH COMMIT: ${mission.name} / ${mission.vehicle}`);
      console.log(`  ${'─'.repeat(50)}`);
      console.log('');

      for (const step of HOLDS) {
        console.log(`  ${step}`);
      }

      console.log('');

      if (mission.outcome === 'RUD') {
        console.log(`  LIFTOFF — ${mission.name} is go for launch!`);
        console.log('');
        console.log('  ...');
        console.log('');
        console.log('  ✗ ANOMALY DETECTED. VEHICLE SAFED.');
        console.log('    (Vehicle achieved maximum disassembly. All data nominal.)');
      } else if (mission.outcome === 'SUCCESS') {
        console.log(`  LIFTOFF — ${mission.name} is go for launch!`);
        console.log('');
        console.log('  ...');
        console.log('');
        console.log('  ✓ MISSION COMPLETE. Vehicle successfully delivered payload.');
        console.log('    (Declared success before splashdown. No notes.)');
      } else {
        console.log(`  LIFTOFF — ${mission.name} is go for launch!`);
        console.log('');
        console.log('  ... (partial success)');
        console.log('');
        console.log('  ~ PARTIAL COMPLETION. Some objectives met.');
        console.log('    (Progress. Officially. On the record.)');
      }

      console.log('');
    });
}
