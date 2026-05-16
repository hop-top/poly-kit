/**
 * commands/telemetry.ts — telemetry get <mission>
 */

import { Command } from 'commander';
import { findMission, pick } from '../data';

interface TelemetryData {
  mission: string;
  vehicle: string;
  altitude_km: number;
  velocity_ms: number;
  downrange_km: number;
  stage: string;
  propellant_remaining_pct: number;
  comms_status: string;
  outcome: string;
  note: string;
}

function generateTelemetry(missionName: string, vehicle: string, outcome: string, note: string): TelemetryData {
  // Pseudo-random but deterministic per mission name
  const seed = missionName.split('').reduce((a, c) => a + c.charCodeAt(0), 0);
  const rand = (min: number, max: number) => min + (seed % (max - min + 1));

  const stages = ['STAGE_1_BURN', 'MECO', 'STAGE_SEP', 'STAGE_2_BURN', 'FAIRING_SEP', 'COAST'];

  let commsStatus: string;
  if (outcome === 'RUD') {
    commsStatus = 'LOSS_OF_SIGNAL (permanent)';
  } else if (outcome === 'PARTIAL') {
    commsStatus = 'DEGRADED';
  } else {
    commsStatus = 'NOMINAL';
  }

  return {
    mission: missionName,
    vehicle,
    altitude_km: rand(80, 550),
    velocity_ms: rand(4000, 7800),
    downrange_km: rand(200, 3000),
    stage: stages[seed % stages.length],
    propellant_remaining_pct: outcome === 'RUD' ? 0 : rand(12, 88),
    comms_status: commsStatus,
    outcome,
    note,
  };
}

function getFormat(cmd: Command): string {
  let c: Command | null = cmd;
  while (c) {
    const opts = c.opts();
    if (opts['format']) return opts['format'] as string;
    c = c.parent;
  }
  return 'table';
}

export function telemetryCommand(): Command {
  const telemetry = new Command('telemetry')
    .description('Mission telemetry streams');

  telemetry
    .command('get <mission>')
    .description('Get telemetry for a mission')
    .action(function (missionName: string) {
      const format = getFormat(this);
      const mission = findMission(missionName);

      if (!mission) {
        process.stderr.write(`  error: unknown mission: "${missionName}"\n`);
        process.stderr.write(`  Run 'spaced mission list' to see available missions.\n`);
        process.exit(1);
      }

      const data = generateTelemetry(mission.name, mission.vehicle, mission.outcome, pick(mission.notes));

      if (format === 'json') {
        console.log(JSON.stringify(data, null, 2));
        return;
      }

      if (format === 'yaml') {
        console.log(`mission: ${data.mission}`);
        console.log(`vehicle: ${data.vehicle}`);
        console.log(`altitude_km: ${data.altitude_km}`);
        console.log(`velocity_ms: ${data.velocity_ms}`);
        console.log(`downrange_km: ${data.downrange_km}`);
        console.log(`stage: ${data.stage}`);
        console.log(`propellant_remaining_pct: ${data.propellant_remaining_pct}`);
        console.log(`comms_status: ${data.comms_status}`);
        console.log(`outcome: ${data.outcome}`);
        console.log(`note: "${data.note}"`);
        return;
      }

      // Table
      console.log('');
      console.log(`  TELEMETRY: ${data.mission}`);
      console.log(`  ${'─'.repeat(50)}`);
      console.log(`  Vehicle:              ${data.vehicle}`);
      console.log(`  Altitude:             ${data.altitude_km} km`);
      console.log(`  Velocity:             ${data.velocity_ms} m/s`);
      console.log(`  Downrange:            ${data.downrange_km} km`);
      console.log(`  Stage:                ${data.stage}`);
      console.log(`  Propellant remaining: ${data.propellant_remaining_pct}%`);
      console.log(`  Comms:                ${data.comms_status}`);
      console.log(`  Outcome:              ${data.outcome}`);
      console.log('');
      console.log(`  Note: ${data.note}`);
      console.log('');
      if (data.outcome === 'RUD') {
        console.log('  ⚠ Telemetry stream terminated. Data archived for RCA.');
        console.log('    RCA timeline: 6–18 months. Updates via press conference.');
        console.log('');
      }
    });

  return telemetry;
}
