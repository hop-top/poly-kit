// Runs the shared completion corpus in contracts/completion-v1. The
// vectors are the source of truth: a failure means this port drifted
// from the contract (Go is canonical), not that a vector should be
// edited to fit.
//
// Strict: unknown fields fail, `version` must be `v1` and every JSON
// file in the corpus must have a runner here.

import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { Command } from 'commander';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { AliasStore, bridgeAliases, findCommand } from './alias';
import { bindArgs, bindFlag, completeRequest, completionCommand } from './completion';

import {
  type Completer,
  type CompletionItem,
  configKeysCompleter,
  dirCompleter,
  fileCompleter,
  funcCompleter,
  prefixedCompleter,
  resolveCompletions,
  staticCompleter,
  staticValues,
} from './completion-values';

const CORPUS = join(__dirname, '..', '..', '..', 'contracts', 'completion-v1');
const FILES = ['completers.json', 'requests.json', 'scripts.json'];

function strictKeys(o: object, allowed: string[], where: string): void {
  for (const k of Object.keys(o)) {
    if (!allowed.includes(k)) throw new Error(`${where}: unknown field ${k}`);
  }
}

function load<T>(name: string): T {
  for (const n of readdirSync(CORPUS)) {
    if (n.endsWith('.json') && !FILES.includes(n)) {
      throw new Error(`${n} has no runner`);
    }
  }
  const f = JSON.parse(readFileSync(join(CORPUS, name), 'utf8'));
  strictKeys(f, ['$schema', '$id', 'version', 'description', 'cases', 'trees'], name);
  if (f.version !== 'v1') throw new Error(`${name}: version ${f.version}`);
  if (!Array.isArray(f.cases) || f.cases.length === 0) {
    throw new Error(`${name}: cases section is empty`);
  }
  return f as T;
}

interface ConfItem { value: string; description?: string }

function item(i: ConfItem): CompletionItem {
  strictKeys(i, ['value', 'description'], 'item');
  return i.description ? { value: i.value, description: i.description } : { value: i.value };
}

/** A one-key completer spec. */
type Spec = Record<string, any>;

function buildCompleter(s: Spec): Completer {
  strictKeys(s, ['static', 'static_values', 'func', 'prefixed', 'config_keys', 'file', 'dir'], 'spec');
  const keys = Object.keys(s);
  if (keys.length !== 1) throw new Error('completer spec needs exactly one key');
  const v = s[keys[0]!];
  switch (keys[0]) {
    case 'static': return staticCompleter(...(v as ConfItem[]).map(item));
    case 'static_values': return staticValues(...(v as string[]));
    case 'func': {
      strictKeys(v, ['items', 'error'], 'func');
      if ((v.items === undefined) === (v.error === undefined)) {
        throw new Error('func spec needs exactly one of items, error');
      }
      return funcCompleter(() => {
        if (v.error !== undefined) throw new Error(v.error);
        return (v.items as ConfItem[]).map(item);
      });
    }
    case 'prefixed':
      strictKeys(v, ['dimension', 'values'], 'prefixed');
      return prefixedCompleter(v.dimension, buildCompleter(v.values));
    case 'config_keys': return configKeysCompleter(v);
    case 'file': return fileCompleter(...(v as string[]));
    default:
      strictKeys(v, [], 'dir');
      return dirCompleter();
  }
}

function same(got: string[], want: string[], unordered: boolean): void {
  if (unordered) {
    expect([...got].sort()).toEqual([...want].sort());
  } else {
    expect(got).toEqual(want);
  }
}

interface CompleterCase {
  name: string;
  completer: Spec;
  prefix: string;
  items?: ConfItem[];
  error?: boolean;
  unordered?: boolean;
  resolved: { completions: string[]; directive: number };
}

describe('completion-v1 conformance: completers.json', () => {
  const f = load<{ cases: CompleterCase[] }>('completers.json');

  it.each(f.cases.map(c => [c.name, c] as const))('%s', async (_n, c) => {
    strictKeys(c, ['name', 'completer', 'prefix', 'items', 'error', 'unordered', 'resolved'], c.name);
    strictKeys(c.resolved, ['completions', 'directive'], c.name);
    const comp = buildCompleter(c.completer);
    const flat = (is: CompletionItem[]) => is.map(i => `${i.value}|${i.description ?? ''}`);
    let items: CompletionItem[] | undefined;
    let failed = false;
    try {
      items = await comp.complete(c.prefix, { commandPath: '', args: [] });
    } catch {
      failed = true;
    }
    expect(failed).toBe(c.error === true);
    if (!failed) {
      same(flat(items!), flat((c.items ?? []).map(item)), c.unordered === true);
    } else {
      expect(c.items).toBeUndefined();
    }
    const r = await resolveCompletions(comp, c.prefix);
    same(r.completions, c.resolved.completions, c.unordered === true);
    expect(r.directive).toBe(c.resolved.directive);
  });
});

// --- requests.json ---

interface ConfFlag { long: string; short?: string; help: string; value: boolean }
interface ConfCommand { name: string; about: string; flags?: ConfFlag[]; commands?: ConfCommand[] }
interface Tree {
  program: string;
  about: string;
  commands: ConfCommand[];
  aliases: Record<string, string>;
  flag_enums: { path: string; flag: string; values: string[] }[];
  flag_completers: { path: string; flag: string; completer: Spec }[];
  arg_completers: { path: string; completer: Spec }[];
}
interface RequestCase {
  name: string;
  tree: string;
  args: string[];
  no_desc?: boolean;
  unordered?: boolean;
  stdout: string[];
  stderr_contains?: string;
}

/**
 * Cases this port cannot pass yet, with the reason. The TS SDK has no
 * completion-spec exporter, so `completion fig|usage` do not exist.
 */
const KNOWN_GAPS: Record<string, string> = {
  'completion command lists its shells':
    'no TS completion-spec exporter: `completion fig|usage` missing',
};

function buildCommand(c: ConfCommand): Command {
  strictKeys(c, ['name', 'about', 'flags', 'commands'], c.name);
  const cmd = new Command(c.name).description(c.about);
  for (const f of c.flags ?? []) {
    strictKeys(f, ['long', 'short', 'help', 'value'], `${c.name} --${f.long}`);
    const names = f.short ? `-${f.short}, --${f.long}` : `--${f.long}`;
    cmd.option(f.value ? `${names} <value>` : names, f.help);
  }
  for (const child of c.commands ?? []) cmd.addCommand(buildCommand(child));
  return cmd;
}

function walk(cmd: Command, fn: (c: Command) => void): void {
  fn(cmd);
  for (const c of cmd.commands) walk(c, fn);
}

/** A kit root as the Go runner builds it, in commander terms. */
function buildRoot(t: Tree): Command {
  strictKeys(t, ['program', 'about', 'commands', 'aliases', 'flag_enums',
    'flag_completers', 'arg_completers'], t.program);
  const program = new Command(t.program).description(t.about);
  for (const c of t.commands) program.addCommand(buildCommand(c));
  const store = new AliasStore('');
  for (const [name, target] of Object.entries(t.aliases)) store.set(name, target);
  bridgeAliases(program, store);
  // A flag enum is commander's choices: tree-wide first, then the
  // command-scoped declarations, which win.
  const ordered = [...t.flag_enums].sort((a, b) => Number(a.path !== '') - Number(b.path !== ''));
  for (const e of ordered) {
    strictKeys(e, ['path', 'flag', 'values'], 'flag_enums');
    const scope = e.path ? findCommand(program, e.path)! : program;
    walk(scope, c => {
      if (e.path && c !== scope) return;
      c.options.find(o => o.long === `--${e.flag}`)?.choices(e.values);
    });
  }
  // As in cobra, the built-in completion command yields to a command
  // or alias of that name.
  const taken = program.commands.some(c =>
    c.name() === 'completion' || c.aliases().includes('completion'));
  if (!taken) program.addCommand(completionCommand(program));
  for (const fc of t.flag_completers) {
    strictKeys(fc, ['path', 'flag', 'completer'], 'flag_completers');
    bindFlag(findCommand(program, fc.path)!, fc.flag, buildCompleter(fc.completer));
  }
  for (const ac of t.arg_completers) {
    strictKeys(ac, ['path', 'completer'], 'arg_completers');
    bindArgs(findCommand(program, ac.path)!, buildCompleter(ac.completer));
  }
  return program;
}

describe('completion-v1 conformance: requests.json', () => {
  const f = load<{ trees: Record<string, Tree>; cases: RequestCase[] }>('requests.json');
  const roots = new Map(Object.entries(f.trees).map(([n, t]) => [n, buildRoot(t)]));

  it('every tree has a case', () => {
    const used = new Set(f.cases.map(c => c.tree));
    expect(Object.keys(f.trees).filter(n => !used.has(n))).toEqual([]);
  });

  for (const [name, reason] of Object.entries(KNOWN_GAPS)) {
    it.skip(`${name} (known gap: ${reason})`, () => {});
  }

  const cases = f.cases.filter(c => !(c.name in KNOWN_GAPS));
  it.each(cases.map(c => [c.name, c] as const))('%s', async (_n, c) => {
    strictKeys(c, ['name', 'tree', 'args', 'no_desc', 'unordered', 'stdout', 'stderr_contains'], c.name);
    const root = roots.get(c.tree);
    expect(root, `no tree ${c.tree}`).toBeDefined();
    let out = '';
    let err = '';
    await completeRequest(root!, c.args, c.no_desc === true, {
      out: s => { out += s; },
      err: s => { err += s; },
    });
    const got = out.replace(/\n$/, '').split('\n');
    const want = [...c.stdout];
    expect(got.pop()).toBe(want.pop());
    same(got, want, c.unordered === true);
    if (c.stderr_contains) expect(err).toContain(c.stderr_contains);
  });

  it('known gaps name real cases', () => {
    const names = new Set(f.cases.map(c => c.name));
    expect(Object.keys(KNOWN_GAPS).filter(n => !names.has(n))).toEqual([]);
  });
});

// --- scripts.json ---

interface ScriptCase {
  name: string;
  program: string;
  shell: string;
  no_descriptions: boolean;
  golden: string;
}

describe('completion-v1 conformance: scripts.json', () => {
  const f = load<{ cases: ScriptCase[] }>('scripts.json');
  afterEach(() => { vi.restoreAllMocks(); });

  it('every golden is claimed by a case', () => {
    const goldens = readdirSync(join(CORPUS, 'scripts'))
      .filter(n => n !== 'README.md').map(n => `scripts/${n}`);
    const claimed = new Set(f.cases.map(c => c.golden));
    expect(goldens.filter(g => !claimed.has(g))).toEqual([]);
  });

  it.each(f.cases.map(c => [c.name, c] as const))('%s', async (_n, c) => {
    strictKeys(c, ['name', 'program', 'shell', 'no_descriptions', 'golden'], c.name);
    const want = readFileSync(join(CORPUS, c.golden), 'utf8');
    const program = new Command(c.program).description('Kit app');
    program.command('sub').description('A subcommand');
    program.addCommand(completionCommand(program));
    let got = '';
    vi.spyOn(process.stdout, 'write').mockImplementation((s: string | Uint8Array) => {
      got += String(s);
      return true;
    });
    const argv = ['completion', c.shell, ...(c.no_descriptions ? ['--no-descriptions'] : [])];
    await program.parseAsync(argv, { from: 'user' });
    vi.restoreAllMocks();
    expect(got).toBe(want);
  });
});
