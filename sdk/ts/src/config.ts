/**
 * @packageDocumentation
 *
 * Layered YAML configuration loader — TypeScript port of hop.top/kit/config.
 *
 * Merges configuration from up to three file layers in priority order:
 *   system → user → project → envOverride
 *
 * Each layer merges into `dst` with these rules:
 *
 * - Plain objects merge recursively, key by key, so a later layer only
 *   overrides the keys it sets (`db: {port: 2}` keeps an earlier `db.host`).
 * - Arrays and scalars are replaced wholesale.
 * - `null` clears an object or array; it leaves a scalar untouched.
 * - On a type conflict (object vs non-object) the later value replaces the
 *   earlier one.
 * - Non-plain objects (`Date`, `Map`, class instances) are never merged
 *   into; a later value replaces them.
 *
 * This matches Go's `kit/core/config` for struct fields. Go differs where its
 * target type differs: a `map[...]` value or an `any` field is replaced
 * whole rather than merged, and a type conflict on a typed field is a decode
 * error rather than a replacement.
 *
 * Missing files are silently skipped. A file that exists but contains invalid
 * YAML causes {@link load} to throw. A document whose root is not a mapping
 * is ignored.
 *
 * Each file is checked before it merges, with aliases expanded, and
 * {@link load} throws `config: failed to parse <path>: <reason>` when:
 *
 * - its aliases expand past Go yaml.v3's budget: more than 100 nodes inside
 *   alias expansions and more than 1000 in total, with the share inside
 *   aliases above 0.99 (up to 400k nodes), falling linearly to 0.10 at 4M
 *   (`document contains excessive aliasing`). Go counts only the keys its
 *   target struct reads; TS has no target, so it counts every key, and a
 *   file Go accepts because the aliases sit under ignored keys fails here.
 * - collections nest deeper than 100, counted through aliases
 *   (`nesting exceeded maxDepth (100)`). Go allows 10000 levels of text
 *   nesting and caps alias chains only through the budget above.
 *
 * @example
 * ```ts
 * import { load } from "@hop-top/kit/config";
 *
 * interface AppConfig {
 *   host: string;
 *   port: number;
 *   debug: boolean;
 *   token: string;
 * }
 *
 * const cfg: AppConfig = { host: "localhost", port: 3000, debug: false, token: "" };
 *
 * load(cfg, {
 *   systemConfigPath: "/etc/mytool/config.yaml",
 *   userConfigPath:   `${process.env.HOME}/.config/mytool/config.yaml`,
 *   projectConfigPath: ".mytool.yaml",
 *   envOverride(c) {
 *     if (process.env.HOST)  c.host  = process.env.HOST;
 *     if (process.env.PORT)  c.port  = Number(process.env.PORT);
 *     if (process.env.TOKEN) c.token = process.env.TOKEN;
 *   },
 * });
 * ```
 */

import * as fs from "fs";
import * as yaml from "js-yaml";

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/**
 * Configuration sources for {@link load}.
 *
 * All path fields are optional; an empty or omitted path is silently skipped.
 * {@link Options.envOverride} is called last, after all file layers, so it can
 * apply environment-variable overrides on top of file values.
 */
export interface Options<T extends object = object> {
  /**
   * Path to the system-wide config file, e.g. `/etc/mytool/config.yaml`.
   * Applied first (lowest priority).
   */
  systemConfigPath?: string;

  /**
   * Path to the per-user config file, e.g. `~/.config/mytool/config.yaml`.
   * Applied second; merges over system values (see the module merge rules).
   */
  userConfigPath?: string;

  /**
   * Path to the project-level config file, e.g. `.mytool.yaml`.
   * Applied third; merges over system and user values.
   */
  projectConfigPath?: string;

  /**
   * Optional callback invoked after all file layers have been merged.
   * Receives the fully-merged `dst` object so the caller can apply
   * environment-variable overrides last (highest priority).
   *
   * @param cfg - The merged config object (same reference as `dst`).
   */
  envOverride?: (cfg: T) => void;
}

/**
 * Merges layered YAML configuration into `dst` and returns it.
 *
 * Merge order: `systemConfigPath` → `userConfigPath` → `projectConfigPath` →
 * `envOverride`.
 *
 * - Objects merge recursively; arrays and scalars replace; `null` clears
 *   objects and arrays but not scalars (see the module docs for details).
 * - Missing files (ENOENT) are silently skipped.
 * - A file that exists but is not valid YAML causes this function to throw
 *   a descriptive error wrapping the parse failure.
 * - `envOverride`, when provided, is called last with the merged `dst`.
 * - Returns `dst` (the same object reference passed in).
 *
 * @param dst  - Destination object to merge config into. Must be a non-null
 *               object; mutated in place.
 * @param opts - Config sources. All fields are optional.
 * @returns The mutated `dst` object.
 * @throws {Error} When a file exists but cannot be parsed as YAML.
 *
 * @example
 * ```ts
 * const cfg = { host: "localhost", port: 8080 };
 * load(cfg, { projectConfigPath: ".tool.yaml" });
 * console.log(cfg.host); // value from .tool.yaml, or "localhost" if file missing
 * ```
 */
export function load<T extends object>(dst: T, opts: Options<T> = {}): T {
  const paths = [
    opts.systemConfigPath,
    opts.userConfigPath,
    opts.projectConfigPath,
  ] as const;

  for (const p of paths) {
    if (!p) continue;
    mergeFile(dst, p);
  }

  if (opts.envOverride) {
    opts.envOverride(dst);
  }

  return dst;
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

/**
 * Read `path`, parse as YAML, and merge into `dst` with {@link mergeInto}.
 * ENOENT is silently ignored; other errors propagate.
 */
function mergeFile<T extends object>(dst: T, filePath: string): void {
  let raw: string;
  try {
    raw = fs.readFileSync(filePath, "utf8");
  } catch (err: unknown) {
    if (isEnoent(err)) return;
    throw err;
  }

  let parsed: unknown;
  try {
    parsed = yaml.load(raw);
    checkBounds(parsed);
  } catch (err: unknown) {
    throw new Error(
      `config: failed to parse ${filePath}: ${err instanceof Error ? err.message : String(err)}`,
    );
  }

  if (isPlainObject(parsed)) {
    mergeInto(dst as Record<string, unknown>, parsed);
  }
}

/**
 * Collection nesting limit, counted through aliases. js-yaml already rejects
 * text nested deeper than its `maxDepth` (default 100, not in its typings),
 * but an alias chain can nest deeper than the text does, and merge/copy
 * recurse once per level.
 */
const MAX_DEPTH = 100;

/** yaml.v3's alias budget (decode.go): see {@link checkBounds}. */
const ALIAS_MIN_ALIASES = 100;
const ALIAS_MIN_DECODED = 1000;
const ALIAS_RATIO_LOW = 400_000;
const ALIAS_RATIO_HIGH = 4_000_000;

function allowedAliasRatio(decoded: number): number {
  if (decoded <= ALIAS_RATIO_LOW) return 0.99;
  if (decoded >= ALIAS_RATIO_HIGH) return 0.1;
  return 0.99 - 0.89 * ((decoded - ALIAS_RATIO_LOW) / (ALIAS_RATIO_HIGH - ALIAS_RATIO_LOW));
}

/**
 * Walks the parsed document the way merge and copy will, aliases expanded,
 * and throws before that work can blow up.
 *
 * js-yaml returns an alias as a second reference to the anchored object, so
 * copying it expands every reference: a "billion laughs" file of a few
 * hundred bytes turns into ~10^9 nodes. js-yaml has no option to cap aliases,
 * so this applies Go yaml.v3's budget: once more than 100 nodes were decoded
 * inside alias expansions and more than 1000 in total, the share decoded
 * inside aliases must stay at or below 0.99 (up to 400k nodes), falling
 * linearly to 0.10 at 4M; otherwise `document contains excessive aliasing`.
 * The first reference to an object is its anchor; later ones count as alias
 * expansions. Scalars are counted but cannot be told apart from aliased
 * scalars. Unlike Go, which skips keys its target struct ignores, every key
 * counts. Iterative, so depth never touches the call stack.
 */
function checkBounds(root: unknown): void {
  const seen = new WeakSet<object>();
  // [node, depth, inside an alias expansion]
  const stack: [unknown, number, boolean][] = [[root, 1, false]];
  let decoded = 0;
  let aliased = 0;
  while (stack.length > 0) {
    const [node, depth, inAlias] = stack.pop() as [unknown, number, boolean];
    let alias = inAlias;
    const container = Array.isArray(node) || isPlainObject(node);
    if (container) {
      if (seen.has(node)) {
        decoded++; // the alias node itself, as yaml.v3 counts it
        alias = true;
      } else {
        seen.add(node);
      }
    }
    decoded++;
    if (alias) aliased++;
    if (
      aliased > ALIAS_MIN_ALIASES &&
      decoded > ALIAS_MIN_DECODED &&
      aliased / decoded > allowedAliasRatio(decoded)
    ) {
      throw new Error("document contains excessive aliasing");
    }
    if (!container) continue;
    if (depth > MAX_DEPTH) {
      throw new Error(`nesting exceeded maxDepth (${MAX_DEPTH})`);
    }
    if (Array.isArray(node)) {
      for (let i = node.length - 1; i >= 0; i--) stack.push([node[i], depth + 1, alias]);
    } else {
      const keys = Object.keys(node);
      for (let i = keys.length - 1; i >= 0; i--) {
        decoded++; // the key node
        if (alias) aliased++;
        stack.push([(node as Record<string, unknown>)[keys[i]], depth + 1, alias]);
      }
    }
  }
}

/**
 * Merges `src` into `dst` in place: plain objects recurse, everything else
 * replaces, and `null` clears only objects and arrays.
 */
function mergeInto(dst: Record<string, unknown>, src: Record<string, unknown>): void {
  for (const key of Object.keys(src)) {
    const next = src[key];
    const has = Object.prototype.hasOwnProperty.call(dst, key);
    const cur = has ? dst[key] : undefined;
    if (next === null) {
      if (has && cur !== null && cur !== undefined && typeof cur !== "object") {
        continue; // null leaves a scalar untouched
      }
      setOwn(dst, key, null);
    } else if (isPlainObject(next) && isPlainObject(cur)) {
      mergeInto(cur, next);
    } else {
      setOwn(dst, key, copy(next));
    }
  }
}

/** Own data property even for keys like `__proto__`, never a setter call. */
function setOwn(dst: Record<string, unknown>, key: string, value: unknown): void {
  Object.defineProperty(dst, key, {
    value,
    writable: true,
    enumerable: true,
    configurable: true,
  });
}

/**
 * Copies plain objects and arrays so YAML aliases do not share one instance
 * in `dst` (a later merge into one key would otherwise leak into another).
 * Memoizing per source object would bring that sharing back, so each
 * reference is copied; {@link checkBounds} has already capped the total.
 */
function copy(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(copy);
  if (isPlainObject(v)) {
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(v)) setOwn(out, k, copy(v[k]));
    return out;
  }
  return v;
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
  if (v === null || typeof v !== "object" || Array.isArray(v)) return false;
  const proto: unknown = Object.getPrototypeOf(v);
  return proto === Object.prototype || proto === null;
}

function isEnoent(err: unknown): boolean {
  return (
    typeof err === "object" &&
    err !== null &&
    "code" in err &&
    (err as NodeJS.ErrnoException).code === "ENOENT"
  );
}
