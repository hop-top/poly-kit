import { mkdtempSync, mkdirSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Command, CommanderError } from 'commander';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { createCLI } from './cli';
import {
  destructiveToken, dryRunApplied, isDryRun, noPrompt, resolveConfirmMode,
  resolveDryRunPolicy, scriptedPrompt, type PromptSource,
} from './delegation';
import { defaultPolicyLoader, Engine, kitDefault, pathMatch, resolvePolicyPath } from './policy';
import { destructiveNames, gateSideEffect, inferredDestructive } from './sideeffect';

interface Leaf {
  name: string;
  se?: string;
  ann?: Record<string, string>;
  opts?: string[];
  run?: (cmd: Command) => void;
}

interface Res { code: number; out: string; err: string; ran: string[] }

function build(leaves: Leaf[], cfg: { prompt?: PromptSource; policy?: boolean } = {}) {
  const ran: string[] = [];
  const { program } = createCLI({
    name: 'kitapp', version: '1.0.0', description: 'd',
    promptSource: cfg.prompt ?? noPrompt,
    policy: cfg.policy ? defaultPolicyLoader('kitapp') : undefined,
  });
  let out = '';
  let err = '';
  program.exitOverride();
  program.configureOutput({
    writeOut: (s) => { out += s; },
    writeErr: (s) => { err += s; },
  });
  for (const l of leaves) {
    const c = program.command(l.name).description(l.name);
    c.exitOverride();
    for (const o of l.opts ?? []) c.option(o);
    const ann = { ...(l.ann ?? {}) };
    if (l.se !== undefined) ann['kit/side-effect'] = l.se;
    (c as unknown as { annotations: Record<string, string> }).annotations = ann;
    c.action(() => { ran.push(l.name); l.run?.(c); });
  }
  const run = (args: string[]): Res => {
    let code = 0;
    try {
      program.parse(['node', 'kitapp', ...args]);
    } catch (e) {
      if (!(e instanceof CommanderError)) throw e;
      code = e.exitCode;
    }
    return { code, out, err, ran };
  };
  return { program, run };
}

let xdg: string;
beforeEach(() => {
  xdg = mkdtempSync(join(tmpdir(), 'ts-gate-'));
  process.env['XDG_CONFIG_HOME'] = xdg;
});
afterEach(() => { delete process.env['XDG_CONFIG_HOME']; });

function policyFile(name: string, body: string): void {
  mkdirSync(join(xdg, 'kitapp', 'policies'), { recursive: true });
  writeFileSync(join(xdg, 'kitapp', 'policies', `${name}.yaml`), body);
}

describe('side-effect inference', () => {
  it('infers only for an absent or empty annotation', () => {
    expect(inferredDestructive('delete', {})).toBe(true);
    expect(inferredDestructive('delete', { 'kit/side-effect': '' })).toBe(true);
    expect(inferredDestructive('delete', { 'kit/side-effect': 'read' })).toBe(false);
    expect(inferredDestructive('delete', { 'kit/side-effect': 'bogus' })).toBe(false);
    expect(inferredDestructive('delete-all', {})).toBe(false);
    expect(gateSideEffect('rm', undefined)).toBe('destructive-shared');
    expect(destructiveNames()).toEqual(['delete', 'destroy', 'drop', 'purge', 'remove', 'rm']);
  });
});

describe('confirm gate', () => {
  const l: Leaf[] = [{ name: 'rm', se: 'destructive' }, { name: 'add', se: 'write' }];

  it('refuses a destructive without a terminal', () => {
    const r = build(l).run(['rm']);
    expect(r.code).toBe(5);
    expect(r.err).toContain('refused: --confirm=no (or non-TTY default)');
    expect(r.ran).toEqual([]);
  });
  it.each(['yes', 'auto'])('--confirm %s lets it run', (m) => {
    const r = build(l).run(['--confirm', m, 'rm']);
    expect(r.code).toBe(0);
    expect(r.ran).toEqual(['rm']);
  });
  it('prompt: y accepts, n aborts', () => {
    expect(build(l, { prompt: scriptedPrompt(['y']) }).run(['--confirm', 'prompt', 'rm']).code).toBe(0);
    const r = build(l, { prompt: scriptedPrompt(['n']) }).run(['--confirm', 'prompt', 'rm']);
    expect(r.code).toBe(5);
    expect(r.err).toContain('aborted by user at confirm prompt for kitapp rm');
  });
  it('unset confirm prompts only when a terminal exists', () => {
    expect(resolveConfirmMode(noPrompt, '')).toBe('no');
    expect(resolveConfirmMode(scriptedPrompt([]), '')).toBe('prompt');
    expect(build(l, { prompt: scriptedPrompt(['yes']) }).run(['rm']).code).toBe(0);
  });
  it('a write needs no confirmation', () => {
    expect(build(l).run(['add']).code).toBe(0);
  });
  it('machine format gets exactly one json envelope', () => {
    const r = build(l).run(['--format', 'json', 'rm']);
    expect(r.code).toBe(5);
    expect(JSON.parse(r.err).code).toBe('UNAUTHORIZED');
  });
  it('a leaf-local --confirm is the leaf mode, as in Go', () => {
    const r = build([{ name: 'rm', se: 'destructive', opts: ['--confirm <m>'] }]).run(['rm', '--confirm', 'yes']);
    expect(r.code).toBe(0);
  });
});

describe('typed token', () => {
  const l: Leaf[] = [{ name: 'purge', se: 'destructive', ann: { 'kit/destructive-token': 'required' } }];
  it('token is first 6 bytes of sha256 of the path', () => {
    expect(destructiveToken('kitapp purge')).toBe('9bcc9064589a');
  });
  it('required, accepted, mismatched', () => {
    const miss = build(l).run(['--confirm', 'yes', 'purge']);
    expect(miss.code).toBe(5);
    expect(miss.err).toContain('requires --confirm-token=9bcc9064589a');
    expect(build(l).run(['purge', '--confirm-token', '9bcc9064589a']).code).toBe(0);
    const bad = build(l).run(['purge', '--confirm-token', 'deadbeef0000']);
    expect(bad.code).toBe(5);
    expect(bad.err).toContain('--confirm-token mismatch for kitapp purge; expected 9bcc9064589a');
  });
  it('machine format carries the fix in the envelope, no prose preview', () => {
    const r = build(l).run(['--format', 'json', 'purge']);
    expect(JSON.parse(r.err).suggested_fix).toContain('--confirm-token=9bcc9064589a');
  });
});

describe('policy', () => {
  const l: Leaf[] = [
    { name: 'add', se: 'write' }, { name: 'ls', se: 'read' }, { name: 'rm', se: 'destructive' },
    { name: 'import', se: 'write', opts: ['--policy <file>'], run: () => {} },
  ];
  it('refuses, and --confirm yes does not lift it', () => {
    policyFile('readonly', 'allow:\n  write: []\n  destructive: []\n');
    expect(build(l, { policy: true }).run(['--policy', 'readonly', 'add']).err).toContain('policy: write not allowed for add');
    const r = build(l, { policy: true }).run(['--policy', 'readonly', '--confirm', 'yes', 'rm']);
    expect(r.code).toBe(5);
    expect(build(l, { policy: true }).run(['--policy', 'readonly', 'ls']).code).toBe(0);
  });
  it('require_confirm lifts a write into the gate', () => {
    policyFile('confirm', 'require_confirm:\n  - add\n');
    expect(build(l, { policy: true }).run(['--policy', 'confirm', 'add']).err).toContain('refused: --confirm=no');
    expect(build(l, { policy: true }).run(['--policy', 'confirm', '--confirm', 'yes', 'add']).code).toBe(0);
  });
  it('unknown and unparsable policies are usage errors', () => {
    policyFile('broken', 'allow: [\n');
    for (const n of ['ghost', 'broken']) {
      const r = build(l, { policy: true }).run(['--policy', n, 'add']);
      expect(r.code).toBe(2);
      expect(r.err).toContain(`policy "${n}"`);
    }
  });
  it('no loader wired: only kit-default resolves', () => {
    expect(build(l).run(['--policy', 'x', 'add']).code).toBe(2);
    const r = build(l).run(['--policy', 'kit-default', 'add']);
    expect(r.code).toBe(5);
    expect(r.err).toContain('policy kit-default:');
  });
  it('validates the name before any filesystem access', () => {
    for (const n of ['../x', join(xdg, 'kitapp', 'policies', 'readonly'), 'a b', 'x'.repeat(65)]) {
      const r = build(l, { policy: true }).run(['--policy', n, 'add']);
      expect(r.code).toBe(2);
      expect(r.err).toContain('invalid policy name');
    }
    expect(() => resolvePolicyPath('t', '..')).toThrow(/invalid policy name/);
  });
  it('a leaf-local --policy is not the delegation policy', () => {
    const r = build(l, { policy: true }).run(['import', '--policy', '/tmp/some.yaml']);
    expect(r.code).toBe(0);
    expect(r.ran).toEqual(['import']);
  });
  it('a root --policy before a shadowing leaf still gates', () => {
    policyFile('readonly', 'allow:\n  write: []\n');
    const r = build(l, { policy: true }).run(['--policy', 'readonly', 'import', '--policy', 'f']);
    expect(r.code).toBe(5);
  });
  it('engine globs, tiers and budget', () => {
    expect(pathMatch('a*', 'abc')).toBe(true);
    expect(pathMatch('a*', 'a/b')).toBe(false);
    const e = new Engine(kitDefault(), 1);
    expect(e.authorize({ verb: 'x', sideEffect: 'destructive-shared' }).allowed).toBe(false);
    e.recordOp();
    expect(() => e.recordOp()).toThrow(/max-ops/);
  });
});

describe('max-ops', () => {
  it('within budget runs; a non-integer is a usage error', () => {
    const l: Leaf[] = [{ name: 'add', se: 'write' }];
    expect(build(l).run(['--max-ops', '1', 'add']).code).toBe(0);
    expect(build(l).run(['--max-ops', 'x', 'add']).code).toBe(2);
  });
});

describe('dry-run', () => {
  const l: Leaf[] = [
    { name: 'ls', se: 'read' }, { name: 'add', se: 'write' }, { name: 'rm', se: 'destructive' },
    { name: 'shell', se: 'interactive' }, { name: 'sync', se: 'write', ann: { 'kit/dry-run': 'opted-out' } },
  ];
  const seen: Record<string, boolean> = {};
  const probe = (name: string): Leaf => ({ name, se: name === 'rm' ? 'destructive' : name === 'ls' ? 'read' : 'write',
    run: (c) => { seen[name] = dryRunApplied(c); seen[`${name}:is`] = isDryRun(c); } });

  it('applies to a write, skips the destructive confirm, no-ops a read', () => {
    const b = build(['add', 'rm', 'ls'].map(probe));
    expect(b.run(['--dry-run', 'add']).code).toBe(0);
    expect(seen['add']).toBe(true);
    expect(b.run(['--dry-run', 'rm']).code).toBe(0);
    expect(seen['rm']).toBe(true);
    expect(b.run(['--dry-run', 'ls']).code).toBe(0);
    expect(seen['ls']).toBe(false);
    expect(seen['ls:is']).toBe(true);
  });
  it('refuses interactive and opted-out leaves', () => {
    const r = build(l).run(['--dry-run', 'shell']);
    expect(r.code).toBe(2);
    expect(r.err).toContain('"kitapp shell" does not accept --dry-run: interactive');
    expect(build(l).run(['--dry-run', 'sync']).err).toContain('explicitly opted out');
    expect(JSON.parse(build(l).run(['--format', 'json', '--dry-run', 'shell']).err).code).toBe('USAGE');
  });
  it('dry-run table', () => {
    expect(resolveDryRunPolicy('delete', {})).toBe('allow');
    expect(resolveDryRunPolicy('list', {})).toBe('reject-untagged');
    expect(resolveDryRunPolicy('delete', { 'kit/side-effect': 'bogus' })).toBe('reject-malformed');
    expect(resolveDryRunPolicy('x', { 'kit/side-effect': 'read' })).toBe('noop');
  });
  it('inferred destructive: kit previews, handler never runs, nothing mutates', () => {
    let mutated = false;
    const b = build([{ name: 'delete', run: () => { mutated = true; } }]);
    const r = b.run(['--dry-run', '--format', 'json', 'delete']);
    expect(r.code).toBe(0);
    expect(mutated).toBe(false);
    expect(r.ran).toEqual([]);
    expect(JSON.parse(r.out).command).toBe('kitapp delete');
  });
  it('inferred destructive under a policy refusal is still refused in dry-run', () => {
    policyFile('nodel', 'allow:\n  destructive: []\n');
    const b = build([{ name: 'delete' }], { policy: true });
    expect(b.run(['--policy', 'nodel', '--dry-run', 'delete']).code).toBe(5);
  });
});

describe('inferred destructive gate', () => {
  const l: Leaf[] = [
    { name: 'delete' }, { name: 'list' }, { name: 'drop', se: 'read' }, { name: 'delete-all' },
  ];
  it('gates the unannotated destructive name, nothing else', () => {
    expect(build(l).run(['delete']).code).toBe(5);
    expect(build(l).run(['--confirm', 'yes', 'delete']).code).toBe(0);
    for (const n of ['list', 'drop', 'delete-all']) expect(build(l).run([n]).code).toBe(0);
  });
  it('the destructive name is the leaf name below a group', () => {
    const { program } = createCLI({ name: 'kitapp', version: '1', description: 'd', promptSource: noPrompt });
    program.exitOverride();
    program.configureOutput({ writeErr: () => {} });
    const g = program.command('widget').exitOverride();
    g.command('remove').exitOverride().action(() => {});
    g.command('list').exitOverride().action(() => {});
    expect(() => program.parse(['node', 'k', 'widget', 'remove'])).toThrow();
    expect(() => program.parse(['node', 'k', 'widget', 'list'])).not.toThrow();
  });
});

describe('malformed side-effect tier', () => {
  it('is refused as usage before the handler runs, unconfirmed or not', () => {
    let mutated = false;
    const mk = () => build([{ name: 'wipe', se: 'destructve', run: () => { mutated = true; } }]);
    for (const args of [['wipe'], ['--confirm', 'yes', 'wipe'], ['--dry-run', 'wipe']]) {
      const r = mk().run(args);
      expect(r.code).toBe(2);
      expect(r.err).toContain('malformed kit/side-effect tag "destructve"');
    }
    expect(mutated).toBe(false);
  });
  it('an explicit malformed tier beats the destructive name too', () => {
    expect(build([{ name: 'purge', se: 'bogus' }]).run(['--confirm', 'yes', 'purge']).code).toBe(2);
  });
  it('the engine fails closed on an unknown non-empty tier', () => {
    const e = new Engine({ ...kitDefault(), allow: undefined, unannotated: undefined }, 0);
    expect(e.authorize({ verb: 'x', sideEffect: 'destructve' }).allowed).toBe(false);
    expect(e.authorize({ verb: 'x', sideEffect: '' }).allowed).toBe(true);
  });
});
