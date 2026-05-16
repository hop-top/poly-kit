/**
 * commands/config.ts — config (show XDG paths + loaded config)
 */

import { Command } from 'commander';
import * as path from 'path';
import { configDir, dataDir, cacheDir, stateDir } from '../../../../sdk/ts/src/xdg';
import { load } from '../../../../sdk/ts/src/config';

interface SpacedConfig {
  default_pad: string;
  default_vehicle: string;
  default_orbit: string;
  favorite_mission: string;
}

function valOrDash(s: string): string {
  return s || '—';
}

function loadConfig(): SpacedConfig {
  const cfg: SpacedConfig = {
    default_pad: '',
    default_vehicle: '',
    default_orbit: '',
    favorite_mission: '',
  };

  load(cfg, {
    userConfigPath: path.join(configDir('spaced'), 'config.yaml'),
  });

  return cfg;
}

export function configCommand(): Command {
  return new Command('config')
    .description('Show spaced configuration and paths')
    .action(() => {
      const cfg = loadConfig();

      console.log('');
      console.log('  ── SPACED CONFIG ──────────────────────────────────');
      console.log(`  Default Pad      : ${valOrDash(cfg.default_pad)}`);
      console.log(`  Default Vehicle  : ${valOrDash(cfg.default_vehicle)}`);
      console.log(`  Default Orbit    : ${valOrDash(cfg.default_orbit)}`);
      console.log(`  Favorite Mission : ${valOrDash(cfg.favorite_mission)}`);
      console.log('');
      console.log('  ── PATHS ──────────────────────────────────────────');
      console.log(`  Config : ${configDir('spaced')}`);
      console.log(`  Data   : ${dataDir('spaced')}`);
      console.log(`  Cache  : ${cacheDir('spaced')}`);
      console.log(`  State  : ${stateDir('spaced')}`);
      console.log('');
    });
}
