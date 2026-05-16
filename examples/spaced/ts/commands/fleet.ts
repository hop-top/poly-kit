/**
 * commands/fleet.ts — fleet list / fleet vehicle inspect <name> --systems
 */

import { Command } from 'commander';
import { VEHICLES, findVehicle } from '../data';

export function fleetCommand(): Command {
  const fleet = new Command('fleet')
    .description('Inspect the SpaceX vehicle fleet');

  // --- fleet list ---
  fleet
    .command('list')
    .description('List all vehicles in the fleet')
    .action(function () {
      const COL = { name: 16, type: 28, status: 16, launches: 10 };

      const header =
        '  ' +
        'VEHICLE'.padEnd(COL.name) +
        'TYPE'.padEnd(COL.type) +
        'STATUS'.padEnd(COL.status) +
        'LAUNCHES';

      const sep = '  ' + '─'.repeat(COL.name + COL.type + COL.status + COL.launches + 4);

      console.log('');
      console.log(header);
      console.log(sep);

      for (const v of VEHICLES) {
        const row =
          '  ' +
          v.name.padEnd(COL.name) +
          v.type.padEnd(COL.type) +
          v.status.padEnd(COL.status) +
          String(v.launches);
        console.log(row);
      }

      console.log('');
      console.log('  * Launch counts approximate. RUDs counted only once. Each.');
      console.log('');
    });

  // --- fleet vehicle ---
  const vehicle = new Command('vehicle')
    .description('Vehicle subcommands');

  vehicle
    .command('inspect <name>')
    .description('Inspect a vehicle by name')
    .option('--systems <value>', 'Comma-separated systems to show (default: all)', '')
    .action(function (name: string) {
      const opts = this.opts();
      const v = findVehicle(name);

      if (!v) {
        process.stderr.write(`  error: unknown vehicle: "${name}"\n`);
        process.stderr.write(`  Run 'spaced fleet list' to see available vehicles.\n`);
        process.exit(1);
      }

      const filterSystems = opts['systems']
        ? (opts['systems'] as string).split(',').map((s: string) => s.trim().toLowerCase()).filter(Boolean)
        : [];

      console.log('');
      console.log(`  VEHICLE: ${v.name}`);
      console.log(`  Type:      ${v.type}`);
      console.log(`  Status:    ${v.status}`);
      console.log(`  Reusable:  ${v.reusable ? 'Yes' : 'No'}`);
      console.log(`  Launches:  ${v.launches}`);
      console.log('');
      console.log(`  Fun fact: ${v.fun_fact}`);
      console.log('');

      const systemKeys = filterSystems.length > 0
        ? Object.keys(v.systems).filter(k => filterSystems.includes(k.toLowerCase()))
        : Object.keys(v.systems);

      if (systemKeys.length > 0) {
        console.log('  Systems:');
        for (const key of systemKeys) {
          console.log(`    ${key.padEnd(20)} ${v.systems[key]}`);
        }
        console.log('');
      }
    });

  fleet.addCommand(vehicle);

  return fleet;
}
