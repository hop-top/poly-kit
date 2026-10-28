import { Command } from 'commander';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MISSIONS, findMission } from './data';
import { missionCommand } from './commands/mission';

// Runs `spaced [--format <f>] mission <args>` in process and returns stdout.
async function run(args: string[], format?: string): Promise<string> {
  const out: string[] = [];
  vi.spyOn(console, 'log').mockImplementation((...parts: unknown[]) => {
    out.push(parts.join(' '));
  });
  const root = new Command('spaced').option('--format <f>').exitOverride();
  root.addCommand(missionCommand());
  const argv = format ? ['--format', format, 'mission', ...args] : ['mission', ...args];
  await root.parseAsync(argv, { from: 'user' });
  return out.join('\n');
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('findMission', () => {
  it('matches names case-insensitively', () => {
    const first = MISSIONS[0];
    expect(findMission(first.name.toUpperCase())).toBe(first);
  });

  it('returns undefined for an unknown mission', () => {
    expect(findMission('no such mission')).toBeUndefined();
  });
});

describe('mission list', () => {
  it('prints every mission as JSON with --format json', async () => {
    const parsed = JSON.parse(await run(['list'], 'json')) as { name: string }[];
    expect(parsed.map(m => m.name)).toEqual(MISSIONS.map(m => m.name));
  });
});

describe('mission inspect', () => {
  it('prints the vehicle and outcome of the named mission', async () => {
    const m = MISSIONS[0];
    const out = await run(['inspect', m.name.toLowerCase()]);
    expect(out).toContain(`MISSION: ${m.name}`);
    expect(out).toContain(`Vehicle: ${m.vehicle}`);
    expect(out).toContain(m.outcome);
  });
});
