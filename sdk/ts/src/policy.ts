/**
 * @module policy
 * @package @hop-top/kit
 *
 * The delegation-safety policy: which side-effect classes a named policy
 * lets run, how many mutating operations one invocation may perform, and
 * which commands always ask for confirmation. Ports Go
 * `console/cli/policy` (policy, engine, loader, the shipped `kit-default`).
 *
 * `permissions:` rules are carried and shape-validated but never
 * evaluated (the expression evaluator is Go's `celpermission`), and the
 * `callers:` section is carried and validated but unused: the CLI has no
 * transport-established caller, so only the policy's own rules answer.
 */

import * as fs from 'node:fs';
import * as path from 'node:path';
import * as yaml from 'js-yaml';

import { configDir } from './xdg.js';

/** Name of the policy kit ships; reserved, never a file. */
export const KIT_DEFAULT_NAME = 'kit-default';

/** Tiers of the legacy vocabulary a policy's `allow` map is keyed by. */
type Class = 'read' | 'write' | 'destructive' | 'interactive';

/** One `permissions:` entry; carried and shape-validated, not evaluated. */
export interface PermissionRule {
  name: string;
  when: string;
  effect: string;
  otherwise: string;
  message?: string;
}

/** One `callers:` entry; carried and validated, unused on the CLI. */
export interface CallerRule {
  principal?: string;
  tenant?: string;
  scope?: string;
  allow?: Record<string, string[]>;
  max_ops?: number;
  window?: string;
  require_declared_scope?: string[];
}

export interface Policy {
  name: string;
  /** Per-class verb glob lists. An empty list refuses the class. */
  allow?: Record<string, string[]>;
  /** Per-invocation cap on mutating ops; 0 means unlimited. */
  max_ops: number;
  /** Command-path globs that always require confirmation. */
  require_confirm: string[];
  permissions: PermissionRule[];
  /** Class a command declaring no side-effect is authorized as. */
  unannotated?: string;
  callers: CallerRule[];
  /** Appended to every refusal; set by shipped policies only. */
  remedy?: string;
}

/** Thrown by {@link Engine.recordOp} once the budget is hit. */
export class MaxOpsExceededError extends Error {
  constructor() {
    super('policy: max-ops budget exceeded');
    this.name = 'MaxOpsExceededError';
  }
}

/** A policy with no rules: default-permit. */
export function emptyPolicy(): Policy {
  return { name: '', max_ops: 0, require_confirm: [], permissions: [], callers: [] };
}

/**
 * The policy kit ships: a command declaring no side effect is a write; a
 * caller nobody established reads only; an established principal reads and
 * writes and runs a destructive command that declares a permissions scope.
 */
export function kitDefault(): Policy {
  return {
    name: KIT_DEFAULT_NAME,
    unannotated: 'write',
    allow: { write: [], destructive: [] },
    max_ops: 0,
    require_confirm: [],
    permissions: [],
    callers: [
      {
        allow: { write: ['*'], destructive: ['*'] },
        require_declared_scope: ['destructive'],
      },
    ],
    remedy:
      "kit's default beyond loopback: writes need an authenticated caller, " +
      'destructive commands a declared kit/permissions scope; ' +
      'name a --policy to choose otherwise',
  };
}

/** Maps a tier onto its legacy class; undefined outside kit's vocabulary. */
export function classOf(tier: string): Class | undefined {
  switch (tier) {
    case 'read':
    case 'write':
    case 'destructive':
    case 'interactive':
      return tier;
    case 'write-local':
    case 'write-shared':
      return 'write';
    case 'destructive-local':
    case 'destructive-shared':
      return 'destructive';
    default:
      return undefined;
  }
}

// ---------------------------------------------------------------------------
// path.Match
// ---------------------------------------------------------------------------

/**
 * Shell-style match with Go `path.Match` semantics: `*` any run of
 * non-`/` characters, `?` one non-`/` character, `[...]` a class with
 * ranges and `^` negation, `\` escapes. A malformed pattern matches
 * nothing.
 */
export function pathMatch(pattern: string, name: string): boolean {
  let re = '^';
  for (let i = 0; i < pattern.length; i++) {
    const c = pattern[i]!;
    if (c === '*') {
      re += '[^/]*';
    } else if (c === '?') {
      re += '[^/]';
    } else if (c === '\\') {
      i++;
      if (i >= pattern.length) return false;
      re += escapeRe(pattern[i]!);
    } else if (c === '[') {
      let j = i + 1;
      let cls = '[';
      if (pattern[j] === '^') {
        cls += '^';
        j++;
      }
      let n = 0;
      for (;; j++) {
        if (j >= pattern.length) return false;
        const d = pattern[j]!;
        if (d === ']' && n > 0) break;
        n++;
        if (d === '\\') {
          j++;
          if (j >= pattern.length) return false;
          cls += escapeRe(pattern[j]!);
        } else if (d === '-' && n > 1 && pattern[j + 1] !== ']') {
          cls += '-';
        } else {
          cls += escapeRe(d);
        }
      }
      re += cls + ']';
      i = j;
    } else {
      re += escapeRe(c);
    }
  }
  try {
    return new RegExp(re + '$', 's').test(name);
  } catch {
    return false;
  }
}

function escapeRe(c: string): string {
  return c.replace(/[\\^$.*+?()[\]{}|/-]/g, '\\$&');
}

function matchAny(patterns: readonly string[], value: string): boolean {
  for (const p of patterns) {
    if (p === '*') return true;
    if (pathMatch(p, value)) return true;
    // "delete:*" is the documented shorthand for the verb and its subverbs.
    if (p.endsWith(':*')) {
      const prefix = p.slice(0, -2);
      if (value === prefix || value.startsWith(prefix + ' ')) return true;
    }
  }
  return false;
}

// ---------------------------------------------------------------------------
// Engine
// ---------------------------------------------------------------------------

/** What the engine reads about the command being authorized. */
export interface CommandRef {
  /** The command path minus the root name (`widget add`). */
  verb: string;
  /** The resolved tier (declared or inferred); undefined when untagged. */
  sideEffect?: string;
}

export interface Verdict {
  allowed: boolean;
  requireConfirm: boolean;
  reason: string;
}

function lookupClass(
  allow: Record<string, string[]> | undefined,
  tier: string,
): string[] | undefined {
  if (!allow) return undefined;
  if (Object.prototype.hasOwnProperty.call(allow, tier)) return allow[tier];
  const cls = classOf(tier);
  if (cls !== undefined && cls !== tier && Object.prototype.hasOwnProperty.call(allow, cls)) {
    return allow[cls];
  }
  return undefined;
}

/**
 * Per-invocation enforcement state: {@link Engine.authorize} each command
 * before it runs, {@link Engine.recordOp} after a successful mutating run.
 */
export class Engine {
  private ops = 0;
  private readonly max: number;

  /** `maxOpsOverride > 0` takes precedence over the policy's `max_ops`. */
  constructor(
    readonly policy: Policy,
    maxOpsOverride = 0,
  ) {
    this.max = maxOpsOverride > 0 ? maxOpsOverride : policy.max_ops;
  }

  maxOps(): number {
    return this.max;
  }

  opsCount(): number {
    return this.ops;
  }

  authorize(cmd: CommandRef): Verdict {
    const se = cmd.sideEffect ?? this.policy.unannotated;
    // Untagged and unaddressed by the policy: defer to the validator.
    if (se === undefined || se === '') return { allowed: true, requireConfirm: false, reason: '' };
    if (se === 'read') return { allowed: true, requireConfirm: false, reason: '' };
    // Fail closed: a tier outside kit's vocabulary matches no class.
    if (classOf(se) === undefined) {
      return {
        allowed: false,
        requireConfirm: false,
        reason: this.refusal(`policy: unknown side-effect ${JSON.stringify(se)} for ${cmd.verb}`),
      };
    }
    const patterns = lookupClass(this.policy.allow, se);
    if (patterns !== undefined && !matchAny(patterns, cmd.verb)) {
      return {
        allowed: false,
        requireConfirm: false,
        reason: this.refusal(`policy: ${se} not allowed for ${cmd.verb}`),
      };
    }
    return {
      allowed: true,
      requireConfirm: matchAny(this.policy.require_confirm, cmd.verb),
      reason: '',
    };
  }

  /** Accounts a successful mutating op; throws once the budget is hit. */
  recordOp(): void {
    this.ops++;
    if (this.max > 0 && this.ops > this.max) throw new MaxOpsExceededError();
  }

  private refusal(reason: string): string {
    const r = this.policy.remedy;
    return r ? `${reason} (policy ${this.policy.name}: ${r})` : reason;
  }
}

// ---------------------------------------------------------------------------
// Loader
// ---------------------------------------------------------------------------

const VALID_NAME = /^[A-Za-z0-9_-]{1,64}$/;

/** Resolves a policy name to a Policy; adopters supply one to the CLI. */
export type PolicyLoader = (name: string) => Policy;

/**
 * The canonical path of a named policy:
 * `$XDG_CONFIG_HOME/<tool>/policies/<name>.yaml`. A name is a file stem,
 * never a path: anything outside `[A-Za-z0-9_-]{1,64}` is refused before
 * any filesystem access.
 */
export function resolvePolicyPath(tool: string, name: string): string {
  if (tool === '') throw new Error('policy: tool name required for XDG resolution');
  if (name === '') throw new Error('policy: policy name required');
  if (!VALID_NAME.test(name)) {
    throw new Error(`policy: invalid policy name ${JSON.stringify(name)} (want [A-Za-z0-9_-]{1,64})`);
  }
  return path.join(configDir(tool), 'policies', `${name}.yaml`);
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function strList(v: unknown, what: string): string[] {
  if (v === undefined || v === null) return [];
  if (!Array.isArray(v) || v.some((x) => typeof x !== 'string')) {
    throw new Error(`${what}: want a list of strings`);
  }
  return v as string[];
}

function allowMap(v: unknown, what: string): Record<string, string[]> | undefined {
  if (v === undefined || v === null) return undefined;
  if (!isRecord(v)) throw new Error(`${what}: want a mapping of class to patterns`);
  const out: Record<string, string[]> = {};
  for (const [k, list] of Object.entries(v)) out[k] = strList(list, `${what}.${k}`);
  return out;
}

function int(v: unknown, what: string): number {
  if (v === undefined || v === null) return 0;
  if (typeof v !== 'number' || !Number.isInteger(v)) throw new Error(`${what}: want an integer`);
  return v;
}

/** Shape-checks a `permissions:` block (names unique, effects allow|deny). */
export function validatePermissionRules(rules: readonly PermissionRule[]): void {
  const seen = new Set<string>();
  rules.forEach((r, i) => {
    if (!r.name) throw new Error(`permissions[${i}]: name required`);
    if (seen.has(r.name)) throw new Error(`permission rule ${JSON.stringify(r.name)}: duplicate name`);
    seen.add(r.name);
    if (!r.when) throw new Error(`permission rule ${JSON.stringify(r.name)}: 'when' required`);
    for (const [field, value] of [
      ['effect', r.effect],
      ['otherwise', r.otherwise],
    ] as const) {
      if (value === '' || value === undefined) {
        throw new Error(`permission rule ${JSON.stringify(r.name)}: ${field} required (allow|deny)`);
      }
      if (value !== 'allow' && value !== 'deny') {
        throw new Error(
          `permission rule ${JSON.stringify(r.name)}: ${field} ${JSON.stringify(value)} invalid (want allow|deny)`,
        );
      }
    }
  });
}

function parsePolicy(doc: unknown): Policy {
  if (doc === null || doc === undefined) return emptyPolicy();
  if (!isRecord(doc)) throw new Error('want a mapping at the top level');
  const p = emptyPolicy();
  if (doc['name'] !== undefined && doc['name'] !== null) {
    if (typeof doc['name'] !== 'string') throw new Error('name: want a string');
    p.name = doc['name'];
  }
  const allow = allowMap(doc['allow'], 'allow');
  if (allow) p.allow = allow;
  p.max_ops = int(doc['max_ops'], 'max_ops');
  p.require_confirm = strList(doc['require_confirm'], 'require_confirm');
  if (doc['unannotated'] !== undefined && doc['unannotated'] !== null) {
    if (typeof doc['unannotated'] !== 'string') throw new Error('unannotated: want a string');
    p.unannotated = doc['unannotated'];
  }
  const perms = doc['permissions'];
  if (perms !== undefined && perms !== null) {
    if (!Array.isArray(perms) || perms.some((r) => !isRecord(r))) {
      throw new Error('permissions: want a list of rules');
    }
    p.permissions = perms.map((r) => {
      const rec = r as Record<string, unknown>;
      const s = (k: string): string => (typeof rec[k] === 'string' ? (rec[k] as string) : '');
      return { name: s('name'), when: s('when'), effect: s('effect'), otherwise: s('otherwise'), message: s('message') };
    });
    validatePermissionRules(p.permissions);
  }
  const callers = doc['callers'];
  if (callers !== undefined && callers !== null) {
    if (!Array.isArray(callers) || callers.some((r) => !isRecord(r))) {
      throw new Error('callers: want a list of rules');
    }
    p.callers = callers.map((r, i) => {
      const rec = r as Record<string, unknown>;
      const at = `callers[${i}]`;
      const rule: CallerRule = {};
      for (const k of ['principal', 'tenant', 'scope', 'window'] as const) {
        if (rec[k] !== undefined && rec[k] !== null) {
          if (typeof rec[k] !== 'string') throw new Error(`${at}.${k}: want a string`);
          rule[k] = rec[k] as string;
        }
      }
      const ra = allowMap(rec['allow'], `${at}.allow`);
      if (ra) rule.allow = ra;
      rule.max_ops = int(rec['max_ops'], `${at}.max_ops`);
      rule.require_declared_scope = strList(rec['require_declared_scope'], `${at}.require_declared_scope`);
      return rule;
    });
  }
  validate(p);
  return p;
}

function validate(p: Policy): void {
  const errs: string[] = [];
  if (p.unannotated && classOf(p.unannotated) === undefined) {
    errs.push(`unannotated: unknown side-effect class ${JSON.stringify(p.unannotated)}`);
  }
  p.callers.forEach((r, i) => {
    for (const c of Object.keys(r.allow ?? {})) {
      if (classOf(c) === undefined) errs.push(`callers[${i}]: allow: unknown side-effect class ${JSON.stringify(c)}`);
    }
    for (const c of r.require_declared_scope ?? []) {
      if (classOf(c) === undefined) {
        errs.push(`callers[${i}]: require_declared_scope: unknown side-effect class ${JSON.stringify(c)}`);
      }
    }
    if ((r.max_ops ?? 0) < 0) errs.push(`callers[${i}]: max_ops ${r.max_ops} is negative; 0 means no budget`);
  });
  if (errs.length > 0) throw new Error(errs.join('\n'));
}

/**
 * Reads a YAML policy file. A missing `name:` defaults to the file stem so
 * audit output has a handle.
 */
export function loadPolicy(file: string): Policy {
  let data: string;
  try {
    data = fs.readFileSync(file, 'utf8');
  } catch (e) {
    throw new Error(`policy: load ${file}: ${(e as Error).message}`);
  }
  let p: Policy;
  try {
    p = parsePolicy(yaml.load(data));
  } catch (e) {
    throw new Error(`policy: parse ${file}: ${(e as Error).message}`);
  }
  if (p.name === '') p.name = path.basename(file).replace(/\.(yaml|yml)$/, '');
  return p;
}

/**
 * Resolves and loads a named policy. {@link KIT_DEFAULT_NAME} is reserved:
 * it always returns {@link kitDefault} and no file is read.
 */
export function loadNamedPolicy(tool: string, name: string): Policy {
  if (name === KIT_DEFAULT_NAME) return kitDefault();
  return loadPolicy(resolvePolicyPath(tool, name));
}

/** A loader resolving names against `$XDG_CONFIG_HOME/<tool>/policies/`. */
export function defaultPolicyLoader(tool: string): PolicyLoader {
  return (name) => loadNamedPolicy(tool, name);
}
