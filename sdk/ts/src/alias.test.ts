import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import * as fs from 'fs';
import * as path from 'path';
import * as os from 'os';
import {
  AliasStore, loadFrom, saveTo, findFirstNonFlag, Expander,
  bridgeAliases, findCommand,
} from './alias';
import { Command } from 'commander';

let tmpDir: string;

beforeEach(() => {
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'alias-test-'));
});

afterEach(() => {
  fs.rmSync(tmpDir, { recursive: true, force: true });
});

function writeConfig(name: string, expansion: string): string {
  const cfgDir = path.join(tmpDir, '.tool');
  fs.mkdirSync(cfgDir, { recursive: true });
  const p = path.join(cfgDir, 'config.yaml');
  fs.writeFileSync(p, `aliases:\n  ${name}: ${expansion}\n`);
  return p;
}

function makeExpander(localPath: string): Expander {
  return new Expander({
    globalPath: '/nonexistent/global.yaml',
    localPath,
    seededAliases: { setup: 'config interactive' },
    builtins: new Set(['task', 'config', 'help']),
  });
}

// ── AliasStore ──────────────────────────────────────────────

describe('AliasStore', () => {
  it('load/save round-trip', async () => {
    const p = path.join(tmpDir, 'cfg', 'config.yaml');
    const store = new AliasStore(p);
    store.set('x', 'y');
    await store.save();

    const store2 = new AliasStore(p);
    await store2.load();
    expect(store2.get('x')).toBe('y');
  });

  it('set/get/remove', () => {
    const store = new AliasStore(path.join(tmpDir, 'c.yaml'));
    store.set('tl', 'task list');
    expect(store.get('tl')).toBe('task list');
    store.remove('tl');
    expect(store.get('tl')).toBeUndefined();
  });

  it('all() returns copy', () => {
    const store = new AliasStore(path.join(tmpDir, 'c.yaml'));
    store.set('a', 'b');
    const copy = store.all();
    copy['a'] = 'changed';
    expect(store.get('a')).toBe('b');
  });

  it('expand replaces first arg', () => {
    const store = new AliasStore(path.join(tmpDir, 'c.yaml'));
    store.set('tl', 'task list');
    const result = store.expand(['tool', 'tl', '--mine']);
    expect(result).toEqual(['tool', 'task', 'list', '--mine']);
  });

  it('expand no match returns original', () => {
    const store = new AliasStore(path.join(tmpDir, 'c.yaml'));
    const args = ['tool', 'task', 'show'];
    expect(store.expand(args)).toEqual(args);
  });

  it('expand with flag defaults', () => {
    const store = new AliasStore(path.join(tmpDir, 'c.yaml'));
    store.set('dp', 'deploy --env prod --dry-run');
    const result = store.expand(['tool', 'dp', 'starman']);
    expect(result).toEqual(['tool', 'deploy', '--env', 'prod', '--dry-run', 'starman']);
  });

  it('expand preserves user override args', () => {
    const store = new AliasStore(path.join(tmpDir, 'c.yaml'));
    store.set('dp', 'deploy --env prod');
    const result = store.expand(['tool', 'dp', 'starman', '--env', 'staging']);
    expect(result).toEqual([
      'tool', 'deploy', '--env', 'prod', 'starman', '--env', 'staging',
    ]);
  });

  it('expand with boolean flag defaults', () => {
    const store = new AliasStore(path.join(tmpDir, 'c.yaml'));
    store.set('dp', 'deploy --dry-run');
    const result = store.expand(['tool', 'dp', 'starman', '--no-dry-run']);
    expect(result).toEqual([
      'tool', 'deploy', '--dry-run', 'starman', '--no-dry-run',
    ]);
  });

  it('expand alias no flags user adds flags', () => {
    const store = new AliasStore(path.join(tmpDir, 'c.yaml'));
    store.set('d', 'deploy');
    const result = store.expand(['tool', 'd', 'starman', '--env', 'prod', '--dry-run']);
    expect(result).toEqual([
      'tool', 'deploy', 'starman', '--env', 'prod', '--dry-run',
    ]);
  });

  it('expand with empty args', () => {
    const store = new AliasStore(path.join(tmpDir, 'c.yaml'));
    expect(store.expand(['tool'])).toEqual(['tool']);
  });

  it('load from non-existent file is empty', async () => {
    const store = new AliasStore('/nonexistent/x.yaml');
    await store.load();
    expect(store.all()).toEqual({});
  });
});

// ── loadFrom / saveTo ───────────────────────────────────────

describe('loadFrom', () => {
  it('reads aliases from YAML', () => {
    const p = writeConfig('tl', 'task list');
    const m = loadFrom(p);
    expect(m['tl']).toBe('task list');
  });

  it('returns empty for non-existent file', () => {
    const m = loadFrom('/nonexistent/config.yaml');
    expect(m).toEqual({});
  });

  it('returns empty when no aliases key', () => {
    const p = path.join(tmpDir, 'config.yaml');
    fs.writeFileSync(p, 'other: value\n');
    expect(loadFrom(p)).toEqual({});
  });
});

describe('saveTo', () => {
  it('round-trip', () => {
    const p = path.join(tmpDir, 'cfg', 'config.yaml');
    saveTo(p, { x: 'y' });
    expect(loadFrom(p)['x']).toBe('y');
  });

  it('preserves other keys', () => {
    const p = path.join(tmpDir, 'config.yaml');
    fs.writeFileSync(p, 'other: value\n');
    saveTo(p, { x: 'y' });
    const data = fs.readFileSync(p, 'utf-8');
    expect(data).toContain('other: value');
    expect(data).toContain("x: 'y'");
  });

  it('empty aliases removes section', () => {
    const p = path.join(tmpDir, 'config.yaml');
    saveTo(p, { x: 'y' });
    saveTo(p, {});
    const data = fs.readFileSync(p, 'utf-8');
    expect(data).not.toContain('aliases');
  });
});

// ── findFirstNonFlag ────────────────────────────────────────

describe('findFirstNonFlag', () => {
  const cases: Array<{ name: string; slice: string[]; idx: number; val: string }> = [
    { name: 'simple', slice: ['cmd'], idx: 0, val: 'cmd' },
    { name: 'short flag consumes next', slice: ['-v', 'cmd'], idx: -1, val: '' },
    { name: 'flag=val', slice: ['--config=/tmp', 'cmd'], idx: 1, val: 'cmd' },
    { name: 'flag val', slice: ['-c', '/tmp', 'cmd'], idx: 2, val: 'cmd' },
    { name: 'only flags', slice: ['-v', '--debug'], idx: -1, val: '' },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      const [idx, val] = findFirstNonFlag(tc.slice);
      expect(idx).toBe(tc.idx);
      expect(val).toBe(tc.val);
    });
  }
});

// ── Expander ────────────────────────────────────────────────

describe('Expander', () => {
  it('no match', () => {
    const p = writeConfig('tl', 'task list');
    const e = makeExpander(p);
    const args = ['tool', 'task', 'show', 'T-0001'];
    const [got, ok] = e.expand(args);
    expect(ok).toBe(false);
    expect(got).toEqual(args);
  });

  it('match', () => {
    const p = writeConfig('tl', 'task list');
    const e = makeExpander(p);
    const [got, ok] = e.expand(['tool', 'tl']);
    expect(ok).toBe(true);
    expect(got).toEqual(['tool', 'task', 'list']);
  });

  it('match with extra args', () => {
    const p = writeConfig('tl', 'task list');
    const e = makeExpander(p);
    const [got, ok] = e.expand(['tool', 'tl', '--status', 'OPEN']);
    expect(ok).toBe(true);
    expect(got).toEqual(['tool', 'task', 'list', '--status', 'OPEN']);
  });

  it('flag before alias', () => {
    const p = writeConfig('tl', 'task list');
    const e = makeExpander(p);
    const [got, ok] = e.expand(['tool', '-c', '/tmp/config.yaml', 'tl']);
    expect(ok).toBe(true);
    expect(got).toEqual(['tool', '-c', '/tmp/config.yaml', 'task', 'list']);
  });

  it('empty args', () => {
    const e = makeExpander('/nonexistent');
    const args = ['tool'];
    const [got, ok] = e.expand(args);
    expect(ok).toBe(false);
    expect(got).toEqual(args);
  });

  it('seeded alias', () => {
    const e = makeExpander('/nonexistent');
    const [got, ok] = e.expand(['tool', 'setup']);
    expect(ok).toBe(true);
    expect(got).toEqual(['tool', 'config', 'interactive']);
  });

  it('seeded with trailing args', () => {
    const e = makeExpander('/nonexistent');
    const [got, ok] = e.expand(['tool', 'setup', '--verbose']);
    expect(ok).toBe(true);
    expect(got).toEqual(['tool', 'config', 'interactive', '--verbose']);
  });

  it('expand with flag defaults in alias', () => {
    const p = writeConfig('dp', 'deploy --env prod --dry-run');
    const e = makeExpander(p);
    const [got, ok] = e.expand(['tool', 'dp', 'starman']);
    expect(ok).toBe(true);
    expect(got).toEqual(['tool', 'deploy', '--env', 'prod', '--dry-run', 'starman']);
  });

  it('expand preserves user override of alias flag', () => {
    const p = writeConfig('dp', 'deploy --env prod');
    const e = makeExpander(p);
    const [got, ok] = e.expand(['tool', 'dp', 'starman', '--env', 'staging']);
    expect(ok).toBe(true);
    expect(got).toEqual([
      'tool', 'deploy', '--env', 'prod', 'starman', '--env', 'staging',
    ]);
  });

  it('expand with boolean flag negation', () => {
    const p = writeConfig('dp', 'deploy --dry-run');
    const e = makeExpander(p);
    const [got, ok] = e.expand(['tool', 'dp', 'starman', '--no-dry-run']);
    expect(ok).toBe(true);
    expect(got).toEqual([
      'tool', 'deploy', '--dry-run', 'starman', '--no-dry-run',
    ]);
  });

  it('load merges priority', () => {
    const globalPath = path.join(tmpDir, 'global', 'config.yaml');
    saveTo(globalPath, { tl: 'task list', setup: 'global-override' });

    const localPath = path.join(tmpDir, 'local', 'config.yaml');
    saveTo(localPath, { tl: 'task list --mine' });

    const e = new Expander({
      globalPath,
      localPath,
      seededAliases: { setup: 'config interactive' },
    });

    const m = e.load();
    // local overrides global
    expect(m['tl']).toBe('task list --mine');
    // global overrides seeded
    expect(m['setup']).toBe('global-override');
  });

  it('validateName empty', () => {
    const e = makeExpander('/nonexistent');
    expect(() => e.validateName('')).toThrow(/empty/);
  });

  it('validateName whitespace', () => {
    const e = makeExpander('/nonexistent');
    expect(() => e.validateName('a b')).toThrow(/whitespace/);
  });

  it('validateName builtin', () => {
    const e = makeExpander('/nonexistent');
    expect(() => e.validateName('task')).toThrow(/conflicts/);
  });

  it('validateName ok', () => {
    const e = makeExpander('/nonexistent');
    expect(() => e.validateName('tl')).not.toThrow();
  });
});

// ── findCommand ─────────────────────────────────────────────

describe('findCommand', () => {
  it('finds top-level command', () => {
    const program = new Command('test');
    program.command('deploy').description('Deploy');
    const cmd = findCommand(program, 'deploy');
    expect(cmd?.name()).toBe('deploy');
  });

  it('finds nested command', () => {
    const program = new Command('test');
    const task = program.command('task').description('Tasks');
    task.command('list').description('List tasks');
    const cmd = findCommand(program, 'task list');
    expect(cmd?.name()).toBe('list');
  });

  it('returns undefined for missing command', () => {
    const program = new Command('test');
    program.command('deploy').description('Deploy');
    expect(findCommand(program, 'build')).toBeUndefined();
  });

  it('returns undefined for partial nested path', () => {
    const program = new Command('test');
    program.command('task').description('Tasks');
    expect(findCommand(program, 'task list')).toBeUndefined();
  });
});

// ── bridgeAliases ───────────────────────────────────────────

describe('bridgeAliases', () => {
  it('registers alias on target command', () => {
    const program = new Command('test');
    program.command('deploy').description('Deploy');
    const store = new AliasStore('/dev/null');
    store.set('d', 'deploy');
    bridgeAliases(program, store);

    const deploy = program.commands.find(c => c.name() === 'deploy');
    expect(deploy?.aliases()).toContain('d');
  });

  it('registers multiple aliases on same target', () => {
    const program = new Command('test');
    program.command('deploy').description('Deploy');
    const store = new AliasStore('/dev/null');
    store.set('d', 'deploy');
    store.set('dp', 'deploy');
    bridgeAliases(program, store);

    const deploy = program.commands.find(c => c.name() === 'deploy');
    expect(deploy?.aliases()).toContain('d');
    expect(deploy?.aliases()).toContain('dp');
  });

  it('adds hidden proxy for multi-word alias target', () => {
    const program = new Command('test');
    const task = program.command('task').description('Tasks');
    task.command('list').description('List tasks');
    const store = new AliasStore('/dev/null');
    store.set('tl', 'task list');
    bridgeAliases(program, store);

    // multi-word target => proxy command added at top level
    const proxy = program.commands.find(c => c.name() === 'tl');
    expect(proxy).toBeDefined();
    expect(proxy?.description()).toBe('Alias for task list');
  });

  it('adds hidden proxy for unknown target', () => {
    const program = new Command('test');
    const store = new AliasStore('/dev/null');
    store.set('x', 'nonexistent');
    bridgeAliases(program, store);

    const proxy = program.commands.find(c => c.name() === 'x');
    expect(proxy).toBeDefined();
  });

  it('preserves existing aliases on target command', () => {
    const program = new Command('test');
    const deploy = program.command('deploy').description('Deploy');
    deploy.alias('dep');
    const store = new AliasStore('/dev/null');
    store.set('d', 'deploy');
    bridgeAliases(program, store);

    expect(deploy.aliases()).toContain('dep');
    expect(deploy.aliases()).toContain('d');
  });

  it('empty store is a no-op', () => {
    const program = new Command('test');
    program.command('deploy').description('Deploy');
    const store = new AliasStore('/dev/null');
    bridgeAliases(program, store);

    const deploy = program.commands.find(c => c.name() === 'deploy');
    expect(deploy?.aliases()).toEqual([]);
  });
});
