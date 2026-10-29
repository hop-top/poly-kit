/**
 * @packageDocumentation
 *
 * Layered configuration loader (YAML by default) — TypeScript port of
 * hop.top/kit/config.
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
 * {@link mergeLayers} applies the same rules to objects you already parsed,
 * and {@link MergeOptions} (also accepted by {@link load} as `merge`) can
 * relax them: `null` keeps the lower value, objects merge only to a set depth,
 * arrays concatenate or union, globally or per dotted key path. A file in
 * another format goes through {@link Options.parse}.
 *
 * The defaults match Go's `kit/core/config` for struct fields. Go differs where its
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
import * as path from "path";
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

  /**
   * Parser for file contents; YAML when omitted. A function parses every
   * file. A record maps a lowercased extension with its dot (`".json"`;
   * `""` for an extensionless file such as `.toolrc`) to a parser, and files
   * whose extension has no entry parse as YAML. The parser gets the text and
   * the path (so a `.js` parser can `require` the path instead); a throw
   * fails the load like a YAML error, and a result that is not a plain
   * object is ignored. Without a parser a `.js` file parses as YAML text
   * and is never executed.
   */
  parse?: ConfigParser | Record<string, ConfigParser>;

  /**
   * Merge policies for the file layers, `dst` being the lowest layer. They
   * do not touch {@link envOverride}, which assigns whatever it sets.
   * Defaults are the module merge rules.
   */
  merge?: MergeOptions;
}

/** Parses one config file's text; see {@link Options.parse}. */
export type ConfigParser = (text: string, filePath: string) => unknown;

/** What a `null` in a higher layer does; see {@link MergeOptions.nullPolicy}. */
export type NullPolicy = "clear" | "keep-lower";

/** How two arrays at one key combine; see {@link MergeOptions.arrayPolicy}. */
export type ArrayPolicy = "replace" | "concat" | "union";

/**
 * Custom array merge for one path: gets the lower array (already in the
 * result), a copy of the higher array and the dotted path; returns the value
 * to store.
 */
export type ArrayMergeFn = (lower: unknown[], higher: unknown[], path: string) => unknown;

/**
 * Merge policies for {@link mergeLayers} and {@link Options.merge}. All
 * policies act only where a higher layer's key meets a key already in the
 * result; a value with nothing under it (a new key, a type conflict, an
 * object past `maxDepth`) is copied whole, as is.
 */
export interface MergeOptions {
  /**
   * `"clear"` (default): `null` clears an object or array, sets a key that
   * is absent or already `null`, and leaves a scalar untouched.
   * `"keep-lower"`: `null` is skipped, so the lower value stays and an
   * absent key stays absent.
   */
  nullPolicy?: NullPolicy;

  /**
   * How many object levels below the root merge key by key; deeper objects
   * replace whole. `Infinity` (default) merges at every depth, `1` merges the
   * root and the objects directly under it (`{db: {opts: {...}}}` merges
   * `db` but replaces `db.opts`), `0` assigns root keys only, like
   * `Object.assign`. Must be a non-negative integer or `Infinity`.
   */
  maxDepth?: number;

  /**
   * How two arrays at one key combine. `"replace"` (default): the higher
   * array wins. `"concat"`: lower items, then higher items. `"union"`: lower
   * then higher items, keeping the first of equal items, where plain objects
   * and arrays compare structurally (object key order ignored) and other
   * values by SameValueZero. A policy acts only when both sides are arrays;
   * otherwise the higher value replaces.
   */
  arrayPolicy?: ArrayPolicy;

  /**
   * Per-path override of {@link arrayPolicy}: dotted key path from the root
   * (`"scope.hints"`, split on every `.` like the Go SDK's dotted keys, so a
   * key containing a dot cannot be named) to a policy or an
   * {@link ArrayMergeFn}. Matches the exact path only.
   */
  arrayPolicyByPath?: Record<string, ArrayPolicy | ArrayMergeFn>;
}

/**
 * Merges already-parsed layers, lowest priority first, into a new object and
 * returns it. Layers that are not plain objects (`null`, arrays, scalars)
 * are skipped; the layers are never mutated or shared with the result.
 * Defaults follow the module merge rules; {@link MergeOptions} relaxes them.
 *
 * @throws {Error} `config: layer <i>: <reason>` when a layer nests deeper
 *   than 100 levels or contains a cycle, and on an invalid option.
 *
 * @example
 * ```ts
 * const cfg = mergeLayers([globalRc, projectRc], {
 *   nullPolicy: "keep-lower",
 *   maxDepth: 1,
 *   arrayPolicyByPath: { hints: "union" },
 * });
 * ```
 */
export function mergeLayers<T extends object = Record<string, unknown>>(
  layers: readonly unknown[],
  opts: MergeOptions = {},
): T {
  const policy = resolvePolicy(opts);
  const out: Record<string, unknown> = {};
  layers.forEach((layer, i) => {
    if (!isPlainObject(layer)) return;
    try {
      checkBounds(layer);
    } catch (err: unknown) {
      throw new Error(`config: layer ${i}: ${errMessage(err)}`);
    }
    mergeInto(out, layer, policy, 0, "");
  });
  return out as T;
}

/**
 * Merges layered YAML configuration into `dst` and returns it.
 *
 * Merge order: `systemConfigPath` → `userConfigPath` → `projectConfigPath` →
 * `envOverride`.
 *
 * - Objects merge recursively; arrays and scalars replace; `null` clears
 *   objects and arrays but not scalars (see the module docs for details),
 *   unless {@link Options.merge} says otherwise.
 * - Files parse as YAML unless {@link Options.parse} supplies a parser.
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
 * @throws {Error} When a file exists but cannot be parsed, or on an
 *   invalid {@link Options.merge}.
 *
 * @example
 * ```ts
 * const cfg = { host: "localhost", port: 8080 };
 * load(cfg, { projectConfigPath: ".tool.yaml" });
 * console.log(cfg.host); // value from .tool.yaml, or "localhost" if file missing
 * ```
 */
export function load<T extends object>(dst: T, opts: Options<T> = {}): T {
  const policy = resolvePolicy(opts.merge ?? {});
  const paths = [
    opts.systemConfigPath,
    opts.userConfigPath,
    opts.projectConfigPath,
  ] as const;

  for (const p of paths) {
    if (!p) continue;
    mergeFile(dst, p, opts.parse, policy);
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
 * Read `path`, parse it (YAML unless `parse` picks a parser), and merge into
 * `dst` with {@link mergeInto}. ENOENT is silently ignored; other errors
 * propagate.
 */
function mergeFile<T extends object>(
  dst: T,
  filePath: string,
  parse: Options["parse"],
  policy: Policy,
): void {
  let raw: string;
  try {
    raw = fs.readFileSync(filePath, "utf8");
  } catch (err: unknown) {
    if (isEnoent(err)) return;
    throw err;
  }

  let parsed: unknown;
  try {
    const parser = parserFor(parse, filePath);
    parsed = parser ? parser(raw, filePath) : yaml.load(raw);
    checkBounds(parsed);
  } catch (err: unknown) {
    throw new Error(`config: failed to parse ${filePath}: ${errMessage(err)}`);
  }

  if (isPlainObject(parsed)) {
    mergeInto(dst as Record<string, unknown>, parsed, policy, 0, "");
  }
}

function parserFor(parse: Options["parse"], filePath: string): ConfigParser | undefined {
  if (parse === undefined) return undefined;
  if (typeof parse === "function") return parse;
  const ext = path.extname(filePath).toLowerCase();
  return Object.prototype.hasOwnProperty.call(parse, ext) ? parse[ext] : undefined;
}

function errMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** {@link MergeOptions} validated, with defaults filled in. */
interface Policy {
  keepLower: boolean;
  maxDepth: number;
  array: ArrayPolicy;
  byPath: Map<string, ArrayPolicy | ArrayMergeFn>;
}

const ARRAY_POLICIES: readonly string[] = ["replace", "concat", "union"];

function resolvePolicy(opts: MergeOptions): Policy {
  const { nullPolicy = "clear", maxDepth = Infinity, arrayPolicy = "replace", arrayPolicyByPath } = opts;
  if (nullPolicy !== "clear" && nullPolicy !== "keep-lower") {
    throw new Error(`config: invalid nullPolicy ${JSON.stringify(nullPolicy)}`);
  }
  if (maxDepth !== Infinity && !(Number.isInteger(maxDepth) && maxDepth >= 0)) {
    throw new Error(`config: invalid maxDepth ${String(maxDepth)}: want a non-negative integer or Infinity`);
  }
  if (!ARRAY_POLICIES.includes(arrayPolicy)) {
    throw new Error(`config: invalid arrayPolicy ${JSON.stringify(arrayPolicy)}`);
  }
  const byPath = new Map<string, ArrayPolicy | ArrayMergeFn>();
  for (const [p, v] of Object.entries(arrayPolicyByPath ?? {})) {
    if (typeof v !== "function" && !ARRAY_POLICIES.includes(v)) {
      throw new Error(`config: invalid arrayPolicyByPath[${JSON.stringify(p)}] ${JSON.stringify(v)}`);
    }
    byPath.set(p, v);
  }
  return { keepLower: nullPolicy === "keep-lower", maxDepth, array: arrayPolicy, byPath };
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
 * Merges `src` into `dst` in place. With the default policy: plain objects
 * recurse, everything else replaces, and `null` clears only objects and
 * arrays. `depth` counts object levels below the root; `at` is the dotted
 * path of `dst`.
 */
function mergeInto(
  dst: Record<string, unknown>,
  src: Record<string, unknown>,
  policy: Policy,
  depth: number,
  at: string,
): void {
  for (const key of Object.keys(src)) {
    const next = src[key];
    const has = Object.prototype.hasOwnProperty.call(dst, key);
    const cur = has ? dst[key] : undefined;
    if (next === null) {
      if (policy.keepLower) continue; // null never replaces, never adds
      if (has && cur !== null && cur !== undefined && typeof cur !== "object") {
        continue; // null leaves a scalar untouched
      }
      setOwn(dst, key, null);
    } else if (isPlainObject(next) && isPlainObject(cur) && depth < policy.maxDepth) {
      mergeInto(cur, next, policy, depth + 1, at === "" ? key : `${at}.${key}`);
    } else if (Array.isArray(next) && Array.isArray(cur)) {
      setOwn(dst, key, mergeArrays(cur, next, policy, at === "" ? key : `${at}.${key}`));
    } else {
      setOwn(dst, key, copy(next));
    }
  }
}

function mergeArrays(lower: unknown[], higher: unknown[], policy: Policy, at: string): unknown {
  const rule = policy.byPath.get(at) ?? policy.array;
  const items = copy(higher) as unknown[];
  if (typeof rule === "function") return rule(lower, items, at);
  switch (rule) {
    case "concat":
      return [...lower, ...items];
    case "union": {
      const out: unknown[] = [];
      for (const item of [...lower, ...items]) {
        if (!out.some((o) => deepEqual(o, item))) out.push(item);
      }
      return out;
    }
    default:
      return items;
  }
}

/**
 * Structural equality for {@link MergeOptions.arrayPolicy} `"union"`: plain
 * objects (key order ignored) and arrays compare by content, anything else
 * by SameValueZero.
 */
function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b || (a !== a && b !== b)) return true;
  if (Array.isArray(a)) {
    return Array.isArray(b) && a.length === b.length && a.every((v, i) => deepEqual(v, b[i]));
  }
  if (isPlainObject(a) && isPlainObject(b)) {
    const ka = Object.keys(a);
    if (ka.length !== Object.keys(b).length) return false;
    return ka.every((k) => Object.prototype.hasOwnProperty.call(b, k) && deepEqual(a[k], b[k]));
  }
  return false;
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
