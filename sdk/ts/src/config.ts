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
