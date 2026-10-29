// Runs the shared alias corpus in contracts/alias-v1. The vectors are
// the source of truth: a failure means this port drifted from the
// contract, not that a vector should be edited to fit.
//
// Strict: unknown fields fail, `version` must be `v1` and every JSON
// file in the corpus must have a runner here.

import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { Command, CommanderError } from 'commander';
import { describe, expect, it } from 'vitest';

import {
  AliasNameError, type AliasMap, type BridgeOptions,
  bridgeAliases, detectCycles, findCommand, validateAliasName,
} from './alias';

const PORT = 'ts';
const CORPUS = join(__dirname, '..', '..', '..', 'contracts', 'alias-v1');
const FILES = ['bridge.json', 'cycles.json', 'validate.json'];

function strictKeys(o: object, allowed: string[], where: string): void {
  for (const k of Object.keys(o)) {
    if (!allowed.includes(k)) throw new Error(`${where}: unknown field ${k}`);
  }
}

function load<T>(name: string, top: string[]): T {
  for (const n of readdirSync(CORPUS)) {
    if (n.endsWith('.json') && !FILES.includes(n)) {
      throw new Error(`${n} has no runner`);
    }
  }
  const f = JSON.parse(readFileSync(join(CORPUS, name), 'utf8'));
  strictKeys(f, ['version', 'description', 'cases', ...top], name);
  if (f.version !== 'v1') throw new Error(`${name}: version ${f.version}`);
  if (!Array.isArray(f.cases) || f.cases.length === 0) {
    throw new Error(`${name}: cases section is empty`);
  }
  return f as T;
}

// ── validate.json ───────────────────────────────────────────

interface ValidateCase {
  name: string; alias: string; builtins?: string[];
  existing?: AliasMap; expect: string;
}

describe('alias-v1 validate', () => {
  const f = load<{ cases: ValidateCase[] }>('validate.json', []);
  for (const c of f.cases) {
    it(c.name, () => {
      strictKeys(c, ['name', 'alias', 'builtins', 'existing', 'expect'], c.name);
      let got = 'ok';
      try {
        validateAliasName(c.alias, { builtins: c.builtins, existing: c.existing });
      } catch (e) {
        expect(e).toBeInstanceOf(AliasNameError);
        got = (e as AliasNameError).kind;
      }
      expect(got).toBe(c.expect);
    });
  }
});

// ── cycles.json ─────────────────────────────────────────────

describe('alias-v1 cycles', () => {
  const f = load<{ cases: { name: string; aliases: AliasMap; cycles: string[][] }[] }>(
    'cycles.json', [],
  );
  for (const c of f.cases) {
    it(c.name, () => {
      strictKeys(c, ['name', 'aliases', 'cycles'], c.name);
      expect(detectCycles(c.aliases)).toEqual(c.cycles);
    });
  }
});

// ── bridge.json ─────────────────────────────────────────────

interface Node {
  name: string; description: string;
  flags?: { name: string; default: string }[];
  commands?: Node[];
}
interface Entry { kind: string; hidden?: boolean; description?: string }
interface Run {
  ran: string; path?: string; env?: string | null; args?: string[];
  name?: string; target?: string;
}
interface Check {
  alias?: string; argv?: string[]; entry?: Entry; run?: Run; help?: string;
  by_port?: Record<string, Partial<Check>>;
}
interface BridgeCase {
  name: string; aliases: AliasMap;
  options?: { hidden?: boolean; describe?: boolean; help?: string; dispatch?: string };
  skip?: Record<string, string>;
  checks: Check[];
}
interface BridgeFile {
  tree: Node; describe_template: string; cases: BridgeCase[];
}

// What a target or handler saw.
interface Seen {
  ran: string; path?: string; env?: string | null; args?: string[];
  name?: string; target?: string;
}

function build(
  parent: Command, nodes: Node[], prefix: string, rec: { r: Seen },
  descs: Map<string, string>,
): void {
  for (const n of nodes) {
    strictKeys(n, ['name', 'description', 'flags', 'commands'], n.name);
    const path = `${prefix} ${n.name}`.trim();
    descs.set(path, n.description);
    const c = parent.command(n.name).description(n.description);
    const hasEnv = (n.flags ?? []).some(fl => fl.name === 'env');
    for (const fl of n.flags ?? []) c.option(`--${fl.name} <v>`, '', fl.default);
    if (!n.commands?.length) {
      c.argument('[args...]').action((args: string[], opts: Record<string, string>) => {
        rec.r = { ran: 'target', path, env: hasEnv ? opts.env : null, args };
      });
    }
    build(c, n.commands ?? [], path, rec, descs);
  }
}

function bridgeOptions(f: BridgeFile, c: BridgeCase, rec: { r: Seen }): BridgeOptions | undefined {
  const o = c.options;
  if (!o) return undefined;
  strictKeys(o, ['hidden', 'describe', 'help', 'dispatch'], c.name);
  const opts: BridgeOptions = {};
  if (o.hidden !== undefined) opts.hidden = o.hidden;
  if (o.describe) {
    opts.describe = (name, target) =>
      f.describe_template.replace('{name}', name).replace('{target}', target);
  }
  if (o.help === 'own') opts.helpOption = true;
  else if (o.help === 'passthrough') opts.helpOption = false;
  else if (o.help !== undefined) throw new Error(`unknown help ${o.help}`);
  if (o.dispatch === 'handler') {
    opts.dispatch = (name, target, cmd) => {
      rec.r = { ran: 'handler', name, target, args: [...cmd.args] };
    };
  } else if (o.dispatch === 'register-only') {
    opts.dispatch = false;
  } else if (o.dispatch !== undefined) {
    throw new Error(`unknown dispatch ${o.dispatch}`);
  }
  return opts;
}

describe('alias-v1 bridge', () => {
  const f = load<BridgeFile>('bridge.json', ['tree', 'describe_template']);

  function setup(c: BridgeCase) {
    const rec: { r: Seen } = { r: { ran: 'none' } };
    const descs = new Map<string, string>();
    let out = '';
    const program = new Command(f.tree.name)
      .exitOverride()
      .configureOutput({ writeOut: s => { out += s; }, writeErr: s => { out += s; } });
    build(program, f.tree.commands ?? [], '', rec, descs);
    const sorted = Object.fromEntries(Object.entries(c.aliases).sort(([a], [b]) => (a < b ? -1 : 1)));
    bridgeAliases(program, sorted, bridgeOptions(f, c, rec));
    return { program, rec, descs, output: () => out };
  }

  for (const c of f.cases) {
    const skip = c.skip?.[PORT];
    (skip ? it.skip : it)(skip ? `${c.name} (${skip})` : c.name, async () => {
      strictKeys(c, ['name', 'aliases', 'options', 'skip', 'checks'], c.name);
      for (const raw of c.checks) {
        strictKeys(raw, ['alias', 'argv', 'entry', 'run', 'help', 'by_port'], c.name);
        const chk: Check = { ...raw, ...(raw.by_port?.[PORT] ?? {}) };
        const { program, rec, descs, output } = setup(c);

        if (chk.entry) {
          const alias = chk.alias!;
          const proxy = program.commands.find(x => x.name() === alias);
          if (proxy) {
            expect(chk.entry.kind, alias).toBe('proxy');
            if (chk.entry.hidden !== undefined) {
              expect((proxy as unknown as { _hidden: boolean })._hidden, alias)
                .toBe(chk.entry.hidden);
            }
            if (chk.entry.description !== undefined) {
              expect(proxy.description(), alias).toBe(chk.entry.description);
            }
          } else {
            const target = findCommand(program, c.aliases[alias]);
            expect(target?.aliases().includes(alias) ? 'native' : 'missing', alias)
              .toBe(chk.entry.kind);
          }
        } else if (chk.run) {
          await program.parseAsync(chk.argv!, { from: 'user' });
          const r = rec.r;
          expect(r.ran, chk.argv!.join(' ')).toBe(chk.run.ran);
          if (chk.run.ran === 'target') {
            expect({ path: r.path, env: r.env, args: r.args })
              .toEqual({ path: chk.run.path, env: chk.run.env, args: chk.run.args });
          } else if (chk.run.ran === 'handler') {
            expect({ name: r.name, target: r.target, args: r.args })
              .toEqual({ name: chk.run.name, target: chk.run.target, args: chk.run.args });
          }
        } else if (chk.help) {
          const err = await program.parseAsync(chk.argv!, { from: 'user' })
            .then(() => undefined, (e: unknown) => e);
          expect(err, chk.argv!.join(' ')).toBeInstanceOf(CommanderError);
          expect((err as CommanderError).code).toBe('commander.helpDisplayed');
          const alias = chk.argv![0];
          const text = output();
          const proxy = program.commands.find(x => x.name() === alias);
          let got = 'unknown';
          if (text.includes(descs.get(c.aliases[alias])!)) got = 'target';
          else if (proxy && text.includes(proxy.description())) got = 'alias';
          expect(got, text).toBe(chk.help);
          expect(rec.r.ran).toBe('none');
        } else {
          throw new Error(`${c.name}: check has no observable`);
        }
      }
    });
  }
});
