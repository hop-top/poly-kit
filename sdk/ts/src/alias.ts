/**
 * alias.ts — YAML-file-based command alias expansion.
 *
 * Port of Go's cli/alias package. Aliases live in per-tool YAML
 * config files with three-tier priority: seeded < global < local.
 */

import * as fs from 'fs';
import * as path from 'path';
import * as yaml from 'js-yaml';
import { Command } from 'commander';

export type AliasMap = Record<string, string>;

export interface ExpanderConfig {
  globalPath: string;
  localPath: string;
  seededAliases?: AliasMap;
  builtins?: Set<string>;
}

// ── Low-level helpers ───────────────────────────────────────

/** Read aliases from a single YAML config file. */
export function loadFrom(filePath: string): AliasMap {
  let data: string;
  try {
    data = fs.readFileSync(filePath, 'utf-8');
  } catch {
    return {};
  }

  const raw = yaml.load(data) as Record<string, unknown> | null;
  if (!raw || typeof raw !== 'object' || !raw['aliases']) return {};
  return raw['aliases'] as AliasMap;
}

/** Write aliases into a YAML config file, preserving other keys. */
export function saveTo(filePath: string, aliases: AliasMap): void {
  fs.mkdirSync(path.dirname(filePath), { recursive: true, mode: 0o750 });

  let existing: Record<string, unknown> = {};
  try {
    const data = fs.readFileSync(filePath, 'utf-8');
    existing = (yaml.load(data) as Record<string, unknown>) ?? {};
  } catch {
    // file doesn't exist yet
  }

  if (Object.keys(aliases).length === 0) {
    delete existing['aliases'];
  } else {
    existing['aliases'] = { ...aliases };
  }

  fs.writeFileSync(filePath, yaml.dump(existing), { mode: 0o600 });
}

/**
 * Find the first non-flag element in a slice.
 * Returns [index, value]. Flags with `=` are self-contained;
 * short flags without `=` consume the next element as their value.
 */
export function findFirstNonFlag(
  slice: string[],
): [number, string] {
  for (let i = 0; i < slice.length; i++) {
    const a = slice[i];
    if (!a.startsWith('-')) return [i, a];
    if (a.includes('=')) continue;
    // short/long flag consumes next arg as its value
    if (i + 1 < slice.length && !slice[i + 1].startsWith('-')) {
      i++;
    }
  }
  return [-1, ''];
}

// ── AliasStore ──────────────────────────────────────────────

/**
 * Simple single-file alias store with load/save, set/get/remove,
 * and first-arg expansion.
 */
export class AliasStore {
  private filePath: string;
  private aliases: AliasMap = {};

  constructor(filePath: string) {
    this.filePath = filePath;
  }

  load(): void {
    this.aliases = loadFrom(this.filePath);
  }

  save(): void {
    saveTo(this.filePath, this.aliases);
  }

  set(name: string, target: string): void {
    this.aliases[name] = target;
  }

  remove(name: string): void {
    delete this.aliases[name];
  }

  get(name: string): string | undefined {
    return this.aliases[name];
  }

  /** Returns a shallow copy. */
  all(): AliasMap {
    return { ...this.aliases };
  }

  /** Expand first arg if it matches an alias. */
  expand(args: string[]): string[] {
    if (args.length < 2) return args;

    const candidate = args[1];
    const expansion = this.aliases[candidate];
    if (!expansion) return args;

    const parts = expansion.split(/\s+/);
    return [args[0], ...parts, ...args.slice(2)];
  }
}

// ── Expander (multi-file, three-tier) ───────────────────────

/**
 * Three-tier alias expander matching Go's Expander.
 * Priority: seeded < global < local.
 */
export class Expander {
  private cfg: ExpanderConfig;

  constructor(cfg: ExpanderConfig) {
    this.cfg = cfg;
  }

  /** Merged alias map (seeded < global < local). */
  load(): AliasMap {
    const merged: AliasMap = {};

    // seeded (lowest priority)
    if (this.cfg.seededAliases) {
      Object.assign(merged, this.cfg.seededAliases);
    }

    // global overrides seeded
    const global = loadFrom(this.cfg.globalPath);
    Object.assign(merged, global);

    // local overrides global
    const local = loadFrom(this.cfg.localPath);
    Object.assign(merged, local);

    return merged;
  }

  /**
   * Rewrite args if args[1] (or first non-flag arg) matches
   * an alias. Returns [rewrittenArgs, wasExpanded].
   */
  expand(args: string[]): [string[], boolean] {
    if (args.length < 2) return [args, false];

    const aliases = this.load();
    if (Object.keys(aliases).length === 0) return [args, false];

    const [firstNonFlag, candidate] = findFirstNonFlag(args.slice(1));
    if (candidate === '') return [args, false];

    const expansion = aliases[candidate];
    if (!expansion) return [args, false];

    const parts = expansion.split(/\s+/);
    const prefix = args.slice(1, 1 + firstNonFlag);
    const suffix = args.slice(1 + firstNonFlag + 1);

    const result = [args[0], ...prefix, ...parts, ...suffix];
    return [result, true];
  }

  /** Reject names that shadow builtins or contain whitespace. */
  validateName(name: string): void {
    if (name === '') {
      throw new Error('alias name must not be empty');
    }
    if (/\s/.test(name)) {
      throw new Error('alias name must not contain whitespace');
    }
    if (this.cfg.builtins?.has(name)) {
      throw new Error(`alias "${name}" conflicts with a built-in command`);
    }
  }
}

// ── Commander bridge ────────────────────────────────────────

/** Walk the Commander program tree to find a command by space-delimited path. */
export function findCommand(
  program: Command,
  cmdPath: string,
): Command | undefined {
  const parts = cmdPath.split(' ');
  let cmd: Command | undefined = program;
  for (const part of parts) {
    cmd = cmd?.commands.find(c => c.name() === part);
    if (!cmd) return undefined;
  }
  return cmd;
}

/**
 * Register loaded aliases with Commander's native .alias() API.
 *
 * Simple single-command aliases use Commander's built-in .alias(),
 * giving free completion and help display. Multi-word or unresolved
 * targets get a hidden proxy command that re-parses with expanded args.
 */
export function bridgeAliases(
  program: Command,
  store: AliasStore,
): void {
  const all = store.all();
  for (const [name, target] of Object.entries(all)) {
    const parts = target.split(/\s+/);
    // simple alias: single word target that exists as a direct subcommand
    if (parts.length === 1) {
      const targetCmd = findCommand(program, target);
      if (targetCmd) {
        targetCmd.alias(name);
        continue;
      }
    }

    // multi-word target or unknown command: hidden proxy
    const proxy = new Command(name)
      .description(`Alias for ${target}`)
      .allowUnknownOption(true)
      .allowExcessArguments(true)
      .action(function (this: Command) {
        const expanded = target.split(/\s+/);
        program.parse([
          'node', program.name(), ...expanded, ...this.args,
        ]);
      });
    proxy.helpOption(false);
    (proxy as any)._hidden = true;
    program.addCommand(proxy);
  }
}
