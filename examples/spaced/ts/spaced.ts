#!/usr/bin/env node
/**
 * spaced.ts — spaced CLI entry point.
 *
 * A satirical SpaceX CLI historian and parity test vehicle for @hop-top/kit/cli.
 *
 * Not affiliated with, endorsed by, or in any way authorized by SpaceX,
 * Elon Musk, DOGE, NASA, the FAA, or the Starman mannequin currently past Mars.
 * We would, however, accept a sponsorship. Cash, Starlink credits, or a ride
 * on the next Crew Dragon all acceptable. GitHub Sponsors: https://github.com/sponsors/hop-top
 */

import { createCLI, setCommandGroup } from '../../../sdk/ts/src/cli';
import { completionCommand } from '../../../sdk/ts/src/completion';
import { createBus } from '../../../sdk/ts/src/bus';
import { missionCommand }    from './commands/mission';
import { launchCommand }     from './commands/launch';
import { abortCommand }      from './commands/abort';
import { telemetryCommand }  from './commands/telemetry';
import { countdownCommand }  from './commands/countdown';
import { configCommand }     from './commands/config';
import { fleetCommand }      from './commands/fleet';
import { starshipCommand }   from './commands/starship';
import { elonCommand }       from './commands/elon';
import { ipoCommand }        from './commands/ipo';
import { competitorCommand } from './commands/competitor';
import { daemonCommand }     from './commands/daemon';
import { statusCommand }     from './commands/status';
import { toolspecCommand }   from './commands/toolspec';
import { uriCommand }        from './commands/uri';
import { complianceCommand } from './commands/compliance';
import { aliasCommand }      from './commands/alias';
import * as path from 'path';
import { AliasStore, bridgeAliases } from '../../../sdk/ts/src/alias';
import { configDir } from '../../../sdk/ts/src/xdg';

const disclaimer = `Not affiliated with, endorsed by, or in any way authorized by SpaceX,
Elon Musk, DOGE, NASA, the FAA, or the Starman mannequin currently past Mars.
We would, however, accept a sponsorship (https://github.com/sponsors/hop-top).
Cash, Starlink credits, or a ride on the next Crew Dragon all acceptable.`;

const { program } = createCLI({
  name:        'spaced',
  version:     '0.1.0',
  description: 'satirical SpaceX CLI historian — every launch, every RUD, every daemon',
  help:        { disclaimer },
  groups:      [{ id: 'management', title: 'MANAGEMENT', hidden: true }],
});

const bus = createBus();

bus.subscribe('kit.spaced.launch.#', (e) => {
  console.log(`  [bus] ${e.topic} → ${JSON.stringify(e.payload)}`);
});
bus.subscribe('kit.spaced.daemon.#', (e) => {
  console.log(`  [bus] ${e.topic} → ${JSON.stringify(e.payload)}`);
});

// Register all subcommands in alphabetical order
program.addCommand(abortCommand());
const aliasCmd = aliasCommand();
setCommandGroup(aliasCmd, 'management');
program.addCommand(aliasCmd);
program.addCommand(competitorCommand());
const cfgCmd = configCommand();
setCommandGroup(cfgCmd, 'management');
program.addCommand(cfgCmd);
program.addCommand(countdownCommand());
program.addCommand(daemonCommand(bus));
program.addCommand(elonCommand());
program.addCommand(fleetCommand());
program.addCommand(ipoCommand());
program.addCommand(launchCommand(bus));
program.addCommand(missionCommand());
program.addCommand(starshipCommand());
program.addCommand(statusCommand());
program.addCommand(telemetryCommand());
const tsCmd = toolspecCommand();
setCommandGroup(tsCmd, 'management');
program.addCommand(tsCmd);
const complCmd = complianceCommand();
setCommandGroup(complCmd, 'management');
program.addCommand(complCmd);
const uriCmd = uriCommand();
setCommandGroup(uriCmd, 'management');
program.addCommand(uriCmd);
const compCmd = completionCommand(program);
setCommandGroup(compCmd, 'management');
program.addCommand(compCmd);

// Bridge user-defined aliases into Commander's native .alias() API
// so they appear in completions and help.
const aliasStore = new AliasStore(
  path.join(configDir('spaced'), 'config.yaml'),
);
aliasStore.load();
bridgeAliases(program, aliasStore);

program.parse(process.argv);
bus.close();
