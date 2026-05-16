/**
 * @packageDocumentation
 *
 * Layered YAML configuration loader — TypeScript port of hop.top/kit/config.
 *
 * Merges configuration from up to three file layers in priority order:
 *   system → user → project → envOverride
 *
 * Each later layer's values overwrite earlier ones via {@link https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/Object/assign | Object.assign}.
 * Missing files are silently skipped. A file that exists but contains invalid
 * YAML causes {@link load} to throw.
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
   * Applied second; overwrites system values for shared keys.
   */
  userConfigPath?: string;

  /**
   * Path to the project-level config file, e.g. `.mytool.yaml`.
   * Applied third; overwrites system and user values for shared keys.
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
 * Read `path`, parse as YAML, and shallow-merge into `dst`.
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

  if (parsed !== null && parsed !== undefined && typeof parsed === "object") {
    Object.assign(dst, parsed);
  }
}

function isEnoent(err: unknown): boolean {
  return (
    typeof err === "object" &&
    err !== null &&
    "code" in err &&
    (err as NodeJS.ErrnoException).code === "ENOENT"
  );
}
