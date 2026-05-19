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

import { Option } from 'commander';
import { createCLI, setCommandGroup } from '../../../sdk/ts/src/cli';
import { completionCommand } from '../../../sdk/ts/src/completion';
import { createBus } from '../../../sdk/ts/src/bus';
import { Client as TelemetryClient } from '../../../sdk/ts/src/telemetry/client';
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

// --telemetry={off,anon,full} persistent flag. Mirrors the Go-side
// wiring in examples/spaced/go/main.go + telemetry_wiring.go.
//
// Visible in --help; spaced go + py mirror this flag with the same shape
// so the cross-lang parity contract includes --telemetry.
program.addOption(
  new Option('--telemetry <mode>', 'kit-telemetry emit mode (off|anon|full)')
    .choices(['off', 'anon', 'full'])
    .default('off'),
);

// Lazy telemetry client state. We construct on first use so processes
// that never opt in (the common case) pay no JSONL-sink-init cost.
let telemetryClient: TelemetryClient | null = null;
let telemetryMode: 'off' | 'anon' | 'full' = 'off';
let invocationStartTime = 0;

function ensureTelemetryClient(): TelemetryClient | null {
  if (telemetryClient !== null) return telemetryClient;
  try {
    // jsonl sink is the safer default: an https endpoint
    // misconfig must not silently drop events. The Client itself respects
    // KIT_TELEMETRY_SINK / KIT_TELEMETRY_ENDPOINT env overrides.
    telemetryClient = new TelemetryClient({ sink: 'jsonl' });
  } catch {
    // Soft refusal: a Client that won't construct must not crash spaced.
    telemetryClient = null;
  }
  return telemetryClient;
}

// preAction: stamp invocation start + parse --telemetry into module state
// BEFORE the subcommand action fires. Commander resolves the persistent
// option onto the root program's opts() (since we attached it there).
program.hook('preAction', (thisCommand) => {
  invocationStartTime = Date.now();
  const raw = thisCommand.opts().telemetry as string | undefined;
  if (raw === 'anon' || raw === 'full') {
    telemetryMode = raw;
  } else {
    telemetryMode = 'off';
  }
});

// postAction: emit a single `spaced.invocation` event with command path
// + duration. Mirrors the Go-side PersistentPostRunE; exit-code capture
// is a follow-up.
program.hook('postAction', async (_thisCommand, actionCommand) => {
  if (telemetryMode === 'off') return;
  const client = ensureTelemetryClient();
  if (client === null) return;
  // Walk parents to reconstruct the full command path (e.g. ["spaced", "mission", "inspect"]).
  const path: string[] = [];
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  let cur: any = actionCommand;
  while (cur) {
    path.unshift(cur.name());
    cur = cur.parent;
  }
  client.record('spaced.invocation', {
    command_path: path,
    exit_code: 0,
    duration_ms: Date.now() - invocationStartTime,
    kit_version: '0.1.0',
  });
  await client.shutdown(2000);
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
