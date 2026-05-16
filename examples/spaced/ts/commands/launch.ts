/**
 * commands/launch.ts — launch <mission>
 */

import { Command } from 'commander';
import * as fs from 'fs';
import { registerSetFlag, FlagDisplay } from '../../../../sdk/ts/src/flagregister';
import { createLogger } from '../../../../sdk/ts/src/log';
import { Bus, createEvent } from '../../../../sdk/ts/src/bus';
import {
  Wizard, textInput, select, confirm, summary,
  type Option,
} from '../../../../sdk/ts/src/wizard';
import {
  CompletionRegistry,
  staticValues,
  funcCompleter,
} from '../../../../sdk/ts/src/completion-values';
import { findMission, pick, MISSIONS } from '../data';

export function launchCommand(bus?: Bus): Command {
  const cmd = new Command('launch')
    .description('Launch a mission')
    .argument('[mission]', 'Mission name to launch')
    .option('--payload <value>', 'Comma-separated payload manifest (e.g. cargo,crew)', '')
    .option('--orbit <value>', 'Target orbit: leo|geo|lunar|helio|tbd', '')
    .option('--dry-run', 'Simulate launch without igniting engines', false)
    .option('-i, --interactive', 'Run interactive launch wizard', false);
  // Note: -o/--output is registered globally by registerOutputFlags() on the
  // root program; reading it here would shadow the parent flag and cause
  // Commander to leave the local copy undefined. We read rootOpts.output below.

  const tags = registerSetFlag(cmd, 'tag', 'Launch tags', FlagDisplay.Prefix);

  cmd.action(function (missionName: string | undefined) {
      const opts = this.opts();
      const rootOpts = this.parent?.opts() ?? {};
      const logger = createLogger({ quiet: rootOpts.quiet, noColor: rootOpts.color === false });

      if (opts['interactive']) {
        runLaunchWizard();
        return;
      }

      if (!missionName) {
        process.stderr.write('  error: mission name required (or use --interactive)\n');
        process.exit(1);
      }

      bus?.publish(createEvent(
        'kit.spaced.launch.initiated', 'spaced',
        { mission: missionName },
      ));
      logger.info('resolving mission', 'name', missionName);
      const mission = findMission(missionName);

      if (!mission) {
        logger.error('mission not found', 'name', missionName);
        process.stderr.write(`  error: unknown mission: "${missionName}"\n`);
        process.stderr.write(`  Run 'spaced mission list' to see available missions.\n`);
        process.exit(1);
      }

      const payloads = opts['payload']
        ? (opts['payload'] as string).split(',').map((p: string) => p.trim()).filter(Boolean)
        : [];
      const orbit    = (opts['orbit'] as string) || 'leo';
      const dryRun   = opts['dryRun'] as boolean;
      // -o/--output is global (registered by registerOutputFlags on root);
      // commander stores its value on the parent opts, not the subcommand.
      const outFile  = (rootOpts['output'] as string | undefined) || undefined;

      const tagList = tags.values();
      const tagStr = tagList.length > 0 ? tagList.join(', ') : 'none';

      logger.info('launch parameters',
        'vehicle', mission.vehicle, 'orbit', orbit, 'tags', tagStr,
      );

      if (dryRun) {
        logger.warn('dry run mode — no actual launch');
        console.log('');
        console.log('  ── DRY RUN ────────────────────────────────────────────────────────');
        console.log(`  Mission  : ${mission.name}`);
        console.log(`  Vehicle  : ${mission.vehicle}`);
        console.log(`  Orbit    : ${orbit}`);
        console.log(`  Payload  : ${payloads.join(', ') || 'none'}`);
        console.log(`  Tags     : ${tagStr}`);
        console.log('  Status   : Would have launched. Probably would have been fine.');
        console.log('  ──────────────────────────────────────────────────────────────────');
        console.log('');
        console.log('  Dry run complete. No actual rockets were harmed.');
        return;
      }

      // "Launch" the mission.
      console.log('');
      console.log(`  ▶ LAUNCH SEQUENCE INITIATED: ${mission.name}`);
      console.log(`  Vehicle  : ${mission.vehicle}`);
      console.log(`  Orbit    : ${orbit}`);
      console.log(`  Payload  : ${payloads.join(', ') || 'none'}`);
      console.log(`  Tags     : ${tagStr}`);
      console.log(`  T-0      : ${new Date().toISOString()}`);
      console.log(`  Outcome  : ${mission.outcome}`);
      console.log(`  Note     : ${pick(mission.notes)}`);
      console.log('');

      const report = {
        mission: mission.name,
        vehicle: mission.vehicle,
        orbit,
        payload: payloads,
        tags: tagList,
        outcome: mission.outcome,
        note: pick(mission.notes),
        ts: new Date().toISOString(),
      };

      if (outFile) {
        fs.writeFileSync(outFile, JSON.stringify(report, null, 2));
        console.log(`  Report written to ${outFile}`);
      }

      bus?.publish(createEvent('kit.spaced.launch.completed', 'spaced', report));
    });

  // Register value completers
  const reg = new CompletionRegistry();
  reg.register('--orbit', staticValues('leo', 'geo', 'lunar', 'helio', 'tbd'));
  reg.registerArg('launch', 0, funcCompleter((prefix) => {
    const lp = prefix.toLowerCase();
    return MISSIONS
      .filter(m => m.name.toLowerCase().startsWith(lp))
      .map(m => ({ value: m.name, description: m.vehicle }));
  }));
  (cmd as any).__completionRegistry = reg;

  return cmd;
}

/** Headless wizard demo — same steps/defaults as Go. */
function runLaunchWizard(): void {
  const orbitOpts: Option[] = [
    { value: 'leo', label: 'LEO', description: 'Low Earth Orbit' },
    { value: 'geo', label: 'GEO', description: 'Geostationary' },
    { value: 'lunar', label: 'Lunar', description: 'Trans-lunar injection' },
    { value: 'helio', label: 'Helio', description: 'Heliocentric' },
    { value: 'tbd', label: 'TBD', description: 'To be determined' },
  ];

  const w = new Wizard(
    textInput('mission', 'Mission name').withRequired(),
    select('orbit', 'Target orbit', orbitOpts),
    textInput('payload', 'Payload manifest'),
    confirm('dry_run', 'Dry run?').withDefault(false),
    summary('Launch parameters'),
  );

  const defaults: Record<string, unknown> = {
    mission: 'Starlink-42',
    orbit: 'leo',
    payload: '60x Starlink v2 Mini',
    dry_run: true,
  };

  const logger = createLogger({ quiet: false, noColor: false });
  logger.info('wizard: advancing through steps with defaults');
  while (!w.done()) {
    const s = w.current();
    if (s == null) break;

    const val = defaults[s.key];
    if (val === undefined) {
      // Summary step — advance with null.
      w.advance(null);
      continue;
    }
    const [, err] = w.advance(val);
    if (err != null) {
      process.stderr.write(`  error: wizard step "${s.key}": ${err.message}\n`);
      process.exit(1);
    }
  }

  const results = w.results();
  console.log('');
  console.log('  ── WIZARD RESULTS ─────────────────────────────────────────────');
  for (const [k, v] of Object.entries(results)) {
    console.log(`  ${k.padEnd(12)}: ${v}`);
  }
  console.log('  ───────────────────────────────────────────────────────────────');
  console.log('');
  console.log('  Wizard complete. In a real TUI, these would drive the launch.');
}
