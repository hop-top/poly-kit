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
import { registerRootAlias } from './completion';

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

  /**
   * Reject names that shadow builtins or contain whitespace; see
   * {@link validateAliasName}, which needs no config files.
   */
  validateName(name: string): void {
    validateAliasName(name, { builtins: this.cfg.builtins });
  }
}

// ── Standalone validation and cycle detection ───────────────

/** The rule an alias name failed; see {@link validateAliasName}. */
export type AliasNameErrorKind = 'empty' | 'whitespace' | 'builtin' | 'duplicate';

/** An alias name {@link validateAliasName} refused. */
export class AliasNameError extends Error {
  readonly kind: AliasNameErrorKind;
  readonly alias: string;
  /** The existing alias's target, for `duplicate`. */
  readonly target?: string;

  constructor(kind: AliasNameErrorKind, alias: string, target?: string) {
    super(aliasNameMessage(kind, alias, target));
    this.name = 'AliasNameError';
    this.kind = kind;
    this.alias = alias;
    this.target = target;
  }
}

function aliasNameMessage(
  kind: AliasNameErrorKind, alias: string, target?: string,
): string {
  switch (kind) {
    case 'empty': return 'alias name must not be empty';
    case 'whitespace': return 'alias name must not contain whitespace';
    case 'builtin': return `alias "${alias}" conflicts with a built-in command`;
    case 'duplicate':
      return `alias "${alias}" is already registered for "${target ?? ''}"`;
  }
}

/** What {@link validateAliasName} checks a name against. */
export interface AliasNameRules {
  /** Names an alias must not take: the tool's commands and reserved words. */
  builtins?: Iterable<string>;
  /** The alias map the name would join. */
  existing?: AliasMap;
}

const WHITE_SPACE = /\p{White_Space}/u;

/**
 * Check an alias name without a store, config file or command tree.
 * Rules run in order and the first failure throws an
 * {@link AliasNameError}: empty, whitespace (any Unicode White_Space
 * character), builtin, duplicate.
 */
export function validateAliasName(name: string, rules: AliasNameRules = {}): void {
  if (name === '') throw new AliasNameError('empty', name);
  if (WHITE_SPACE.test(name)) throw new AliasNameError('whitespace', name);
  if (rules.builtins) {
    for (const b of rules.builtins) {
      if (b === name) throw new AliasNameError('builtin', name);
    }
  }
  const existing = rules.existing;
  if (existing && Object.prototype.hasOwnProperty.call(existing, name)) {
    throw new AliasNameError('duplicate', name, existing[name]);
  }
}

/**
 * Report alias cycles in `aliases` (name -> target). An alias points at
 * another when the first word of its target is a key of the map. Each
 * cycle is a path starting at its smallest name and ending with it
 * again (`['a', 'b', 'a']`); cycles are sorted by first name. An alias
 * that only leads into a cycle is not part of one.
 */
export function detectCycles(aliases: AliasMap): string[][] {
  const has = (n: string) => Object.prototype.hasOwnProperty.call(aliases, n);
  const next = (n: string): string | undefined => {
    const first = aliases[n].trim().split(/\s+/)[0];
    return first !== '' && has(first) ? first : undefined;
  };
  const cmp = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);

  const done = new Set<string>();
  const cycles: string[][] = [];
  for (const start of Object.keys(aliases).sort(cmp)) {
    if (done.has(start)) continue;
    const pos = new Map<string, number>();
    const walk: string[] = [];
    let cur: string | undefined = start;
    while (cur !== undefined && !done.has(cur)) {
      const at = pos.get(cur);
      if (at !== undefined) {
        const members = walk.slice(at);
        let min = 0;
        members.forEach((m, i) => { if (m < members[min]) min = i; });
        const path = [...members.slice(min), ...members.slice(0, min)];
        cycles.push([...path, path[0]]);
        break;
      }
      pos.set(cur, walk.length);
      walk.push(cur);
      cur = next(cur);
    }
    for (const n of walk) done.add(n);
  }
  return cycles.sort((a, b) => cmp(a[0], b[0]));
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

/** Commander internal used for subcommand dispatch (promise-or-value). */
type Dispatcher = {
  _parseCommand(operands: string[], unknown: string[]): void | Promise<void>;
};

/** Runs an alias proxy in place of its target; `cmd.args` holds every argument after the alias, unparsed. */
export type AliasDispatch = (
  name: string, target: string, cmd: Command,
) => void | Promise<void>;

/**
 * Options for {@link bridgeAliases}. Setting any of them registers every
 * alias as a proxy, a root command named after the alias, even for a
 * single-word target that would otherwise take a Commander alias.
 */
export interface BridgeOptions {
  /** Hide proxies from help. Default true. */
  hidden?: boolean;
  /** A proxy's description, in place of `Alias for <target>`. */
  describe?: (name: string, target: string) => string;
  /**
   * true: the proxy owns -h/--help and shows its own help. false (the
   * default): --help reaches the target, or the dispatch handler.
   */
  helpOption?: boolean;
  /**
   * A handler run instead of the target, or false to register proxies
   * that run nothing (register-only). Default: dispatch the target
   * through the program, like a native subcommand.
   */
  dispatch?: AliasDispatch | false;
}

function hasBridgeOptions(opts?: BridgeOptions): opts is BridgeOptions {
  return opts !== undefined &&
    Object.values(opts).some(v => v !== undefined);
}

/**
 * Register loaded aliases with Commander's native .alias() API.
 *
 * Simple single-command aliases use Commander's built-in .alias(),
 * giving free completion and help display. Multi-word or unresolved
 * targets get a hidden proxy command that dispatches the expanded
 * args from the program, like a native subcommand. With any
 * {@link BridgeOptions} set, every alias gets a proxy shaped by them.
 *
 * Completion offers each alias at most once. A native alias, or a hidden
 * proxy that dispatches its target or runs nothing, is a stand-in:
 * completion lists it as `alias for <target>` and offers a proxy only
 * its help flag. A visible proxy, or one with a dispatch handler, is a
 * real command and completes like any subcommand, with its own flags,
 * including any added after bridging; a hidden one is not listed.
 *
 * `source` is an {@link AliasStore} or a plain alias map, so aliases
 * can come from any storage.
 *
 * Dispatch reuses Commander's own subcommand dispatch rather than
 * calling parse()/parseAsync() again: re-parsing resets program
 * option values mid-parse and fixes the sync/async mode, whereas
 * dispatch returns the target's result as-is. parseAsync() callers
 * thus await async targets (rejections propagate), and parse()
 * callers keep synchronous throws.
 */
export function bridgeAliases(
  program: Command,
  source: AliasStore | AliasMap,
  opts?: BridgeOptions,
): void {
  const all = typeof (source as AliasStore).all === 'function'
    ? (source as AliasStore).all()
    : { ...(source as AliasMap) };
  const proxyAll = hasBridgeOptions(opts);
  // A visible or custom-dispatch proxy is a real command: completion
  // offers it among the subcommands and walks it for its own flags. A
  // root-alias entry would list it twice and complete it as a flagless
  // stand-in.
  const realProxy = proxyAll &&
    (opts.hidden === false || typeof opts.dispatch === 'function');
  for (const [name, target] of Object.entries(all)) {
    if (!realProxy) registerRootAlias(program, name, target);
    const parts = target.split(/\s+/);
    // simple alias: single word target that exists as a direct subcommand
    if (!proxyAll && parts.length === 1) {
      const targetCmd = findCommand(program, target);
      if (targetCmd) {
        targetCmd.alias(name);
        continue;
      }
    }

    // multi-word target, unknown command or options: proxy
    const dispatch = opts?.dispatch;
    const proxy = new Command(name);
    // An options proxy behaves like a subcommand made with
    // program.command(): it inherits exitOverride, output and help
    // settings, which matter once it renders its own help.
    if (proxyAll) proxy.copyInheritedSettings(program);
    proxy
      .description(opts?.describe?.(name, target) ?? `Alias for ${target}`)
      .allowUnknownOption(true)
      .allowExcessArguments(true)
      .action(function (this: Command) {
        if (dispatch === false) return undefined;
        if (dispatch) return dispatch(name, target, this);
        const expanded = target.split(/\s+/);
        return (program as unknown as Dispatcher)._parseCommand(
          [], [...expanded, ...this.args],
        );
      });
    if (opts?.helpOption !== true) proxy.helpOption(false);
    program.addCommand(proxy, { hidden: opts?.hidden ?? true });
  }
}
