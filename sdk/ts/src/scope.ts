/**
 * @packageDocumentation
 *
 * Filesystem path policy guardrails — TypeScript port of `hop.top/kit/go/core/scope`.
 *
 * Mirrors the Go API surface (`New`, `Default`, `Allow`, `Deny`, `Check`,
 * `Enforce`, `SetMode`, `SetDefault`, `Snapshot`, `SecretPaths`). The default
 * deny list is loaded from `contracts/parity/scope-defaults.json` (the
 * cross-language source of truth).
 *
 * Glob matching uses [`picomatch`](https://github.com/micromatch/picomatch)
 * which supports the subset of bash-style globs (including `**`) that the Go
 * port relies on via `bmatcuk/doublestar/v4`.
 *
 * @example
 * ```ts
 * import { Default, Op, Strict } from '@hop-top/kit/scope';
 *
 * const dec = Default().check('/Users/me/.ssh/id_rsa', Op.Read);
 * // -> Decision.Denied
 * ```
 */

import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import picomatch from 'picomatch';

import scopeDefaultsRaw from './scope-defaults.json';

// ─── Types ───────────────────────────────────────────────────────────────────

/** Mode controls how `Enforce` reacts to a `Denied` decision. */
export enum Mode {
  /** Strict denies → throws `ErrDenied`. The default. */
  Strict = 'strict',
  /** Warn denies → logs to stderr and returns. */
  Warn = 'warn',
  /** Prompt denies → invokes the prompt callback. */
  Prompt = 'prompt',
}

/** Op is a bitset of filesystem operations. */
export enum Op {
  Read = 1 << 0,
  Write = 1 << 1,
  Exec = 1 << 2,
}

/** Decision is the outcome of `Check`. */
export enum Decision {
  Unknown = 'unknown',
  Allowed = 'allowed',
  Denied = 'denied',
}

/** A single rule: glob patterns + ops bitset + allow/deny verdict. */
export interface Rule {
  patterns: string[];
  ops: Op;
  allow: boolean;
}

/** Prompt callback used in `Mode.Prompt`. Return `true` to allow this call. */
export type PromptFunc = (path: string, op: Op) => boolean;

/** Error thrown by `Enforce` when a path is denied. */
export class ErrDenied extends Error {
  constructor(p: string, op: Op) {
    super(`scope: path denied: ${p} (op=${opString(op)})`);
    this.name = 'ErrDenied';
  }
}

// ─── Defaults JSON shape ─────────────────────────────────────────────────────

interface ScopeDefaults {
  version: number;
  deny: Record<string, string[]>;
}

const defaults: ScopeDefaults = scopeDefaultsRaw as unknown as ScopeDefaults;

// ─── Policy ──────────────────────────────────────────────────────────────────

/**
 * Policy holds an ordered list of rules, a Mode, and an optional prompt
 * callback. Construct via `New()` or `Default()`.
 */
export class Policy {
  private rules: Rule[] = [];
  private mode: Mode = Mode.Strict;
  private prompt?: PromptFunc;

  /** Register an allow rule covering Read|Write|Exec. */
  allow(...patterns: string[]): this {
    return this.allowOp(Op.Read | Op.Write | Op.Exec, ...patterns);
  }

  /** Register an allow rule for the given operations. */
  allowOp(op: Op, ...patterns: string[]): this {
    if (patterns.length === 0) return this;
    this.rules.push({ patterns: [...patterns], ops: op, allow: true });
    return this;
  }

  /** Register a deny rule covering Read|Write|Exec. */
  deny(...patterns: string[]): this {
    return this.denyOp(Op.Read | Op.Write | Op.Exec, ...patterns);
  }

  /** Register a deny rule for the given operations. */
  denyOp(op: Op, ...patterns: string[]): this {
    if (patterns.length === 0) return this;
    this.rules.push({ patterns: [...patterns], ops: op, allow: false });
    return this;
  }

  /** Set the enforcement mode. */
  setMode(m: Mode): this {
    this.mode = m;
    return this;
  }

  /** Get the current enforcement mode. */
  getMode(): Mode {
    return this.mode;
  }

  /** Set the prompt callback used in `Mode.Prompt`. */
  setPromptFunc(fn: PromptFunc): this {
    this.prompt = fn;
    return this;
  }

  /** Defensive copy of the policy's rules in registration order. */
  getRules(): Rule[] {
    return this.rules.map((r) => ({ patterns: [...r.patterns], ops: r.ops, allow: r.allow }));
  }

  /**
   * Evaluate `(p, op)` against the policy. Pure — no prompt, no mutation.
   * Resolves symlinks before matching to defeat symlink escapes; on ENOENT
   * the cleaned path is used so "intent to write" still matches deny rules.
   */
  check(p: string, op: Op): Decision {
    const resolved = resolvePath(p);
    let sawAllow = false;
    for (const r of this.rules) {
      if ((r.ops & op) === 0) continue;
      if (!matchAny(r.patterns, resolved)) continue;
      if (!r.allow) return Decision.Denied;
      sawAllow = true;
    }
    return sawAllow ? Decision.Allowed : Decision.Unknown;
  }

  /**
   * Enforce calls `check` and translates the decision according to mode:
   * Strict + (Denied|Unknown) throws; Warn + Denied logs; Prompt + Denied
   * invokes the prompt callback.
   */
  enforce(p: string, op: Op): void {
    const dec = this.check(p, op);
    if (dec === Decision.Allowed) return;
    if (dec === Decision.Unknown && this.mode !== Mode.Strict) return;
    this.handleDeny(p, op);
  }

  private handleDeny(p: string, op: Op): void {
    switch (this.mode) {
      case Mode.Warn:
        console.warn(`scope: path denied (warn mode, allowing): ${p} op=${opString(op)}`);
        return;
      case Mode.Prompt:
        if (this.prompt && this.prompt(p, op)) return;
        throw new ErrDenied(p, op);
      default: // Strict
        throw new ErrDenied(p, op);
    }
  }

  /** Deep copy. Useful for per-test isolation paired with `setDefault`. */
  snapshot(): Policy {
    const cp = new Policy();
    cp.mode = this.mode;
    cp.prompt = this.prompt;
    cp.rules = this.rules.map((r) => ({ patterns: [...r.patterns], ops: r.ops, allow: r.allow }));
    return cp;
  }
}

// ─── Singleton + factory ─────────────────────────────────────────────────────

let defaultP: Policy | null = null;

/** Create an empty `Policy` in `Mode.Strict`. */
export function New(): Policy {
  return new Policy();
}

/**
 * Returns the package-level singleton policy used by primitives that don't
 * accept an explicit policy. Lazy-initialised on first call. The singleton is
 * pre-seeded with `SecretPaths()` as a deny-all-ops rule.
 */
export function Default(): Policy {
  if (!defaultP) {
    defaultP = New().deny(...SecretPaths());
  }
  return defaultP;
}

/**
 * Swap the package-level singleton with `p`. Returns a restore function that
 * puts the previous policy back. Intended for tests:
 *
 * ```ts
 * const restore = setDefault(New().allow('~/tmp/**'));
 * try { ... } finally { restore(); }
 * ```
 */
export function setDefault(p: Policy): () => void {
  const prev = Default();
  defaultP = p;
  return () => {
    defaultP = prev;
  };
}

// ─── Default deny list ───────────────────────────────────────────────────────

/**
 * Returns the default deny pattern set: common patterns plus the patterns
 * specific to the current platform. Patterns are ready to feed into
 * `Policy.deny`. `~` and Windows env macros are resolved at match time.
 */
export function SecretPaths(): string[] {
  const common = defaults.deny['common'] ?? [];
  const platName = platformKey();
  const platSpecific = defaults.deny[platName] ?? [];
  return [...common, ...platSpecific].map(expandWindowsEnv);
}

/** Map Node.js `process.platform` to the keys used in scope-defaults.json. */
function platformKey(): string {
  switch (process.platform) {
    case 'darwin':
      return 'darwin';
    case 'win32':
      return 'windows';
    default:
      return 'linux';
  }
}

/**
 * Expand `%APPDATA%`, `%LOCALAPPDATA%`, `%USERPROFILE%` in `p` using the
 * matching env vars when present. On non-Windows hosts the expansion is
 * best-effort: when the env var is empty the macro is left in place and the
 * pattern simply won't match anything.
 */
function expandWindowsEnv(p: string): string {
  for (const key of ['APPDATA', 'LOCALAPPDATA', 'USERPROFILE']) {
    const token = `%${key}%`;
    if (!p.includes(token)) continue;
    const v = process.env[key];
    if (!v) continue;
    p = p.split(token).join(v);
  }
  return p;
}

// ─── Path resolution + match ─────────────────────────────────────────────────

/** Expand a leading `~` to the user's home dir, then `path.normalize`. */
function expandHome(s: string): string {
  if (s === '~' || s.startsWith('~/')) {
    let home = os.homedir();
    try {
      home = fs.realpathSync(home);
    } catch {
      // best effort; keep raw home
    }
    return s === '~' ? home : path.join(home, s.slice(2));
  }
  return s;
}

/**
 * Resolve `s`: expand `~`, normalize, and follow symlinks. On ENOENT walks
 * up to the deepest existing ancestor and re-attaches the missing tail.
 */
function resolvePath(s: string): string {
  const expanded = expandHome(s);
  const cleaned = path.normalize(expanded);
  try {
    return fs.realpathSync(cleaned);
  } catch (err: unknown) {
    if (!isENOENT(err)) return cleaned;
  }
  // Walk up to the deepest existing ancestor.
  let dir = path.dirname(cleaned);
  let tail = path.basename(cleaned);
  while (dir && dir !== path.parse(dir).root) {
    try {
      const resolved = fs.realpathSync(dir);
      return path.join(resolved, tail);
    } catch (err: unknown) {
      if (!isENOENT(err)) return cleaned;
    }
    tail = path.join(path.basename(dir), tail);
    dir = path.dirname(dir);
  }
  return cleaned;
}

function isENOENT(err: unknown): boolean {
  return typeof err === 'object' && err !== null && (err as { code?: string }).code === 'ENOENT';
}

/** Returns true if any of the patterns matches `abs` (already resolved). */
function matchAny(patterns: string[], abs: string): boolean {
  for (const p of patterns) {
    const expanded = expandHome(p);
    const canon = canonicalisePattern(path.normalize(expanded));
    if (picomatch.isMatch(abs, canon, { dot: true })) return true;
  }
  return false;
}

/**
 * Resolve the longest leading literal directory prefix of `pat` through
 * realpath so the pattern keeps matching after the input path is canonicalised.
 */
function canonicalisePattern(pat: string): string {
  const sep = path.sep;
  const parts = pat.split(sep);
  let cut = 0;
  for (let i = 0; i < parts.length; i++) {
    if (containsGlobMeta(parts[i] ?? '')) break;
    cut = i + 1;
  }
  if (cut === 0) return pat;
  const literalParts = parts.slice(0, cut);
  const prefix = literalParts.join(sep);
  if (!prefix) return pat;
  try {
    const resolved = fs.realpathSync(prefix);
    return joinPattern(resolved, parts.slice(cut));
  } catch {
    // Walk up to the deepest existing ancestor.
    for (let i = literalParts.length - 1; i >= 1; i--) {
      const ancestor = literalParts.slice(0, i).join(sep);
      if (!ancestor) continue;
      try {
        const resolved = fs.realpathSync(ancestor);
        const tail = [...literalParts.slice(i), ...parts.slice(cut)];
        return joinPattern(resolved, tail);
      } catch {
        // continue
      }
    }
  }
  return pat;
}

function joinPattern(resolved: string, rest: string[]): string {
  return rest.length === 0 ? resolved : resolved + path.sep + rest.join(path.sep);
}

function containsGlobMeta(s: string): boolean {
  return /[*?[\]{}]/.test(s);
}

function opString(op: Op): string {
  const parts: string[] = [];
  if (op & Op.Read) parts.push('read');
  if (op & Op.Write) parts.push('write');
  if (op & Op.Exec) parts.push('exec');
  return parts.length === 0 ? 'none' : parts.join('|');
}
