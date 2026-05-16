/**
 * commands/starship.ts — starship status / starship history
 */

import { Command } from 'commander';
import { MISSIONS } from '../data';

const STARSHIP_STATUS_NOTES = [
  'Currently undergoing rapid iteration. (Exploding faster.)',
  'FAA awaiting license application. FAA making coffee. FAA will get to it.',
  'Mechazilla operational. Chopstick arms flexing. No regrets.',
  'Super Heavy booster stack complete. Raptor engines counting themselves.',
  'IFT-7 preparations underway. Success probability: optimistic.',
  'Fully reusable target: 2025 (updated from 2022, 2023, 2024). Converging.',
];

export function starshipCommand(): Command {
  const starship = new Command('starship')
    .description('Starship program status and history');

  starship
    .command('status')
    .description('Current Starship program status')
    .action(function () {
      const idx = Date.now() % STARSHIP_STATUS_NOTES.length;

      console.log('');
      console.log('  STARSHIP PROGRAM STATUS');
      console.log(`  ${'─'.repeat(50)}`);
      console.log('');
      console.log('  Vehicle:         Starship + Super Heavy (Ship 30 / Booster 14)');
      console.log('  Pad:             Starbase, Boca Chica, Texas');
      console.log('  Catcher:         Mechazilla (Chopstick Arms v2)');
      console.log('  Catch record:    2/2 booster catches (IFT-5, IFT-6)');
      console.log('  Ship landings:   0 (splashdown × 3, RUD × 3)');
      console.log('  FAA license:     Renewed each flight. Each time, nerve-wracking.');
      console.log('');
      console.log(`  Latest: ${STARSHIP_STATUS_NOTES[idx]}`);
      console.log('');
      console.log('  Company stance: "Exceeding expectations."');
      console.log('  Expectations:   Not publicly disclosed.');
      console.log('');
    });

  starship
    .command('history')
    .description('Starship integrated flight test history')
    .action(function () {
      const starshipMissions = MISSIONS.filter(m => m.vehicle === 'Starship');

      console.log('');
      console.log('  STARSHIP INTEGRATED FLIGHT TEST HISTORY');
      console.log(`  ${'─'.repeat(60)}`);
      console.log('');

      const COL = { name: 10, date: 14, outcome: 12 };
      console.log(
        '  ' +
        'TEST'.padEnd(COL.name) +
        'DATE'.padEnd(COL.date) +
        'OUTCOME'.padEnd(COL.outcome) +
        'NOTES',
      );
      console.log('  ' + '─'.repeat(COL.name + COL.date + COL.outcome + 40));

      for (const m of starshipMissions) {
        const outcomeStr = m.outcome === 'RUD'
          ? '✗ RUD'
          : m.outcome === 'SUCCESS'
            ? '✓ SUCCESS'
            : '~ PARTIAL';
        const shortNote = m.notes[0].slice(0, 45) + (m.notes[0].length > 45 ? '…' : '');
        console.log(
          '  ' +
          m.name.padEnd(COL.name) +
          m.date.padEnd(COL.date) +
          outcomeStr.padEnd(COL.outcome) +
          shortNote,
        );
      }

      console.log('');
      console.log(`  Total IFTs: ${starshipMissions.length}`);
      const successes = starshipMissions.filter(m => m.outcome === 'SUCCESS').length;
      const ruds      = starshipMissions.filter(m => m.outcome === 'RUD').length;
      const partials  = starshipMissions.filter(m => m.outcome === 'PARTIAL').length;
      console.log(`  Successes:  ${successes}   RUDs: ${ruds}   Partial: ${partials}`);
      console.log('');
      console.log('  * All RUDs declared "nominal" by company communications team.');
      console.log('');
    });

  return starship;
}
