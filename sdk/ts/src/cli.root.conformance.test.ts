// Runs the delegation slice of the shared corpus in contracts/cli-root-v1.
// The vectors are the source of truth: a failure means this port drifted
// from the contract (Go is canonical), not that a vector should be edited.
//
// This port implements the delegation gates (--confirm, --confirm-token,
// --max-ops, --policy, --dry-run) and the side-effect inference. Every case
// is either replayed or listed in a skip table with a reason; a case that
// is neither fails the run, so nothing is skipped silently.

import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { Command, CommanderError } from 'commander';
import { describe, expect, it } from 'vitest';

import { createCLI } from './cli';
import { dryRunApplied, noPrompt, scriptedPrompt } from './delegation';
import { defaultPolicyLoader } from './policy';

const CORPUS = join(__dirname, '..', '..', '..', 'contracts', 'cli-root-v1', 'vectors.json');

interface Flag { long: string; help?: string; type?: string }
interface Cmd {
  name: string;
  about: string;
  side_effect?: string;
  annotations?: Record<string, string>;
  dry_run?: string;
  kind?: string;
  flags?: Flag[];
  commands?: Cmd[];
}
interface Tree {
  program: string;
  commands: Cmd[];
  fixtures?: Record<string, string>;
  policy?: boolean;
}
interface Case {
  name: string;
  tree: string;
  args: string[];
  exit: number;
  stdout?: string | null;
  stdout_contains?: string[];
  stderr_contains?: string[];
  stderr_json?: Record<string, unknown>;
  prompt_answer?: string;
}

const file = JSON.parse(readFileSync(CORPUS, 'utf8')) as { version: string; trees: Record<string, Tree>; cases: Case[] };

/** Trees the delegation slice replays; every other tree is skipped whole. */
const REPLAYED = new Set(['ops', 'infer']);

const TREE_SKIP =
  'outside the delegation slice: needs root-flag, alias, validate, signature or config features this port lacks';

/** Cases of a replayed tree that need a feature the TS CLI lacks. */
const CASE_SKIP: Array<[RegExp, string]> = [
  [/^(chdir|no chdir)/, '-C/--chdir resolver not ported'],
  [/^config /, '-c/--config layering and the cfg command not ported'],
  [/^flag validator/, 'flag validators not ported'],
  [
    /^a malformed annotation is not inferred over$/,
    'TS validates side-effect tiers at run time with no disable_validate; Go runs this tree with validation off',
  ],
  [
    /^dry-run still refuses an unannotated destructive-named leaf$/,
    'pending-T-2053: pins the old untagged refusal; TS previews an inferred leaf (kit renders the plan, handler not run)',
  ],
];

function skipReason(c: Case): string | undefined {
  if (!REPLAYED.has(c.tree)) return TREE_SKIP;
  return CASE_SKIP.find(([re]) => re.test(c.name))?.[1];
}

function addCmd(parent: Command, c: Cmd, prefix: string, ran: { value: boolean }): void {
  const path = `${prefix} ${c.name}`.trim();
  const cmd = parent.command(c.name).description(c.about);
  cmd.exitOverride();
  for (const f of c.flags ?? []) cmd.option(f.type === 'bool' ? `--${f.long}` : `--${f.long} <v>`, f.help ?? '');
  const ann: Record<string, string> = { ...(c.annotations ?? {}) };
  if (c.side_effect) ann['kit/side-effect'] = c.side_effect;
  if (c.dry_run === 'opted-out') ann['kit/dry-run'] = 'opted-out';
  (cmd as unknown as { annotations: Record<string, string> }).annotations = ann;
  if ((c.commands ?? []).length > 0) {
    for (const s of c.commands ?? []) addCmd(cmd, s, path, ran);
    return;
  }
  cmd.argument('[args...]');
  cmd.action(() => {
    ran.value = true;
    cmd.configureOutput().writeOut?.(`${JSON.stringify({ dry_run: dryRunApplied(cmd), path })}\n`);
  });
}

function run(c: Case): { code: number; out: string; err: string } {
  const t = file.trees[c.tree]!;
  const tmp = mkdtempSync(join(tmpdir(), 'cli-root-'));
  const sub = (s: string): string => s.split('{tmp}').join(tmp);
  const saved = process.env['XDG_CONFIG_HOME'];
  try {
    for (const [name, body] of Object.entries(t.fixtures ?? {})) {
      const p = join(tmp, name);
      if (name.endsWith('/')) mkdirSync(p, { recursive: true });
      else {
        mkdirSync(dirname(p), { recursive: true });
        writeFileSync(p, sub(body));
      }
    }
    process.env['XDG_CONFIG_HOME'] = join(tmp, 'xdg');
    const { program } = createCLI({
      name: t.program,
      version: '1.2.3',
      description: 'Kit app',
      promptSource: c.prompt_answer !== undefined ? scriptedPrompt([c.prompt_answer]) : noPrompt,
      policy: t.policy ? defaultPolicyLoader(t.program) : undefined,
    });
    let out = '';
    let err = '';
    program.exitOverride();
    program.configureOutput({ writeOut: (s) => { out += s; }, writeErr: (s) => { err += s; } });
    const ran = { value: false };
    for (const cc of t.commands) addCmd(program, cc, '', ran);
    let code = 0;
    try {
      program.parse(['node', t.program, ...c.args.map(sub)]);
    } catch (e) {
      if (!(e instanceof CommanderError)) throw e;
      code = e.exitCode;
    }
    return { code, out, err };
  } finally {
    if (saved === undefined) delete process.env['XDG_CONFIG_HOME'];
    else process.env['XDG_CONFIG_HOME'] = saved;
    rmSync(tmp, { recursive: true, force: true });
  }
}

describe('cli-root-v1 corpus (delegation slice)', () => {
  it('is version v1', () => expect(file.version).toBe('v1'));

  const skipped: Array<[string, string]> = [];
  for (const c of file.cases) {
    const reason = skipReason(c);
    if (reason) {
      skipped.push([c.name, reason]);
      it.skip(`${c.tree}: ${c.name} [skip: ${reason}]`, () => {});
      continue;
    }
    it(`${c.tree}: ${c.name}`, () => {
      const r = run(c);
      expect(r.code, `exit (stderr ${r.err})`).toBe(c.exit);
      if (typeof c.stdout === 'string') expect(r.out).toBe(c.stdout);
      if (c.stdout === null) expect(r.out).toBe('');
      for (const s of c.stdout_contains ?? []) expect(r.out).toContain(s);
      for (const s of c.stderr_contains ?? []) expect(r.err).toContain(s.replace('{tmp}', ''));
      if (c.stderr_json) {
        const j = JSON.parse(r.err) as Record<string, unknown>;
        for (const [k, v] of Object.entries(c.stderr_json)) expect(j[k]).toEqual(v);
      }
    });
  }

  it('every skip carries a reason', () => {
    for (const [, reason] of skipped) expect(reason.length).toBeGreaterThan(0);
  });
});
