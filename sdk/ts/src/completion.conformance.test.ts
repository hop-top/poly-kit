// Runs the shared completion corpus in contracts/completion-v1. The
// vectors are the source of truth: a failure means this port drifted
// from the contract (Go is canonical), not that a vector should be
// edited to fit.
//
// Strict: unknown fields fail, `version` must be `v1` and every JSON
// file in the corpus must have a runner here.

import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

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

export function buildCompleter(s: Spec): Completer {
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

export function same(got: string[], want: string[], unordered: boolean): void {
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
