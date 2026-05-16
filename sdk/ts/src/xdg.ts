/**
 * @packageDocumentation
 *
 * XDG Base Directory path resolution with OS-native fallbacks.
 *
 * Each function checks the corresponding XDG environment variable first
 * (`XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_CACHE_HOME`, `XDG_STATE_HOME`).
 * When the variable is unset or empty it falls back to the platform-native
 * directory:
 *
 * - macOS  (`darwin`):  `~/Library/Application Support/<tool>`  for data/state;
 *                       `~/Library/Preferences`                 for config;
 *                       `~/Library/Caches`                      for cache.
 * - Windows (`win32`):  `%LocalAppData%\<tool>`                 for data/state;
 *                       `%APPDATA%\<tool>`                      for config/cache.
 * - Linux / other:      `~/.config`, `~/.local/share`,
 *                       `~/.cache`, `~/.local/state`.
 *
 * All functions are pure (no side effects). Use {@link mustEnsure} to create
 * the directory on disk.
 *
 * @example
 * ```ts
 * import { configDir, mustEnsure } from '@hop-top/kit/xdg';
 *
 * const cfg = mustEnsure(configDir('mytool'));
 * // cfg is guaranteed to exist on disk
 * ```
 */

import * as os from 'os';
import * as path from 'path';
import * as fs from 'fs';

// ─── Public types ────────────────────────────────────────────────────────────

/**
 * All four canonical XDG-style directories for a single tool, resolved in one
 * shot.
 */
export interface Dirs {
  /** Resolved configuration directory. */
  config: string;
  /** Resolved data directory. */
  data: string;
  /** Resolved cache directory. */
  cache: string;
  /** Resolved state directory. */
  state: string;
}

// ─── Internal helpers ────────────────────────────────────────────────────────

/**
 * Returns the platform string to use for OS-specific fallbacks.
 * Accepts an optional override so callers (and tests) can simulate other
 * platforms without mutating `process.platform`.
 */
function platform(override?: NodeJS.Platform): NodeJS.Platform {
  return override ?? process.platform;
}

// ─── configDir ───────────────────────────────────────────────────────────────

/**
 * Returns the configuration directory for the named tool.
 *
 * Resolution order:
 * 1. `$XDG_CONFIG_HOME/<tool>` — when the variable is set and non-empty.
 * 2. OS-native fallback:
 *    - macOS: `~/Library/Preferences/<tool>`
 *    - Windows: `%APPDATA%\<tool>` (throws if `APPDATA` is unset)
 *    - Linux/other: `~/.config/<tool>`
 *
 * The returned path is **not** guaranteed to exist; call {@link mustEnsure} to
 * create it.
 *
 * @param tool - Tool name appended as the final path segment.
 * @returns Absolute path to the configuration directory.
 * @throws {Error} When the OS-native root cannot be determined
 *   (e.g. `%APPDATA%` is unset on Windows).
 *
 * @example
 * ```ts
 * const dir = configDir('myapp');
 * // macOS  → /Users/alice/Library/Preferences/myapp
 * // Linux  → /home/alice/.config/myapp
 * ```
 */
export function configDir(tool: string, plt?: NodeJS.Platform): string {
  const xdg = process.env['XDG_CONFIG_HOME'];
  if (xdg) return path.join(xdg, tool);

  const p = platform(plt);
  const home = os.homedir();

  switch (p) {
    case 'darwin':
      return path.join(home, 'Library', 'Preferences', tool);
    case 'win32': {
      const appdata = process.env['APPDATA'];
      if (!appdata) throw new Error('%APPDATA% is not set');
      return path.join(appdata, tool);
    }
    default:
      return path.join(home, '.config', tool);
  }
}

// ─── dataDir ─────────────────────────────────────────────────────────────────

/**
 * Returns the data directory for the named tool.
 *
 * Resolution order:
 * 1. `$XDG_DATA_HOME/<tool>` — when the variable is set and non-empty.
 * 2. OS-native fallback:
 *    - macOS: `~/Library/Application Support/<tool>`
 *    - Windows: `%LocalAppData%\<tool>` (throws if `LocalAppData` is unset)
 *    - Linux/other: `~/.local/share/<tool>`
 *
 * @param tool - Tool name appended as the final path segment.
 * @param plt - Optional platform override (defaults to `process.platform`).
 * @returns Absolute path to the data directory.
 * @throws {Error} When the OS-native root cannot be determined.
 *
 * @example
 * ```ts
 * const dir = dataDir('myapp');
 * // macOS   → /Users/alice/Library/Application Support/myapp
 * // Linux   → /home/alice/.local/share/myapp
 * // Windows → C:\Users\alice\AppData\Local\myapp
 * ```
 */
export function dataDir(tool: string, plt?: NodeJS.Platform): string {
  const xdg = process.env['XDG_DATA_HOME'];
  if (xdg) return path.join(xdg, tool);

  const p = platform(plt);
  const home = os.homedir();

  switch (p) {
    case 'darwin':
      return path.join(home, 'Library', 'Application Support', tool);
    case 'win32': {
      const local = process.env['LocalAppData'];
      if (!local) throw new Error('%LocalAppData% is not set');
      return path.win32.join(local, tool);
    }
    default:
      return path.join(home, '.local', 'share', tool);
  }
}

// ─── cacheDir ────────────────────────────────────────────────────────────────

/**
 * Returns the cache directory for the named tool.
 *
 * Resolution order:
 * 1. `$XDG_CACHE_HOME/<tool>` — when the variable is set and non-empty.
 * 2. OS-native fallback:
 *    - macOS: `~/Library/Caches/<tool>`
 *    - Windows: `%LocalAppData%\<tool>\cache` (throws if `LocalAppData` unset)
 *    - Linux/other: `~/.cache/<tool>`
 *
 * @param tool - Tool name appended as the final path segment.
 * @param plt - Optional platform override (defaults to `process.platform`).
 * @returns Absolute path to the cache directory.
 * @throws {Error} When the OS-native root cannot be determined.
 *
 * @example
 * ```ts
 * const dir = cacheDir('myapp');
 * // macOS  → /Users/alice/Library/Caches/myapp
 * // Linux  → /home/alice/.cache/myapp
 * ```
 */
export function cacheDir(tool: string, plt?: NodeJS.Platform): string {
  const xdg = process.env['XDG_CACHE_HOME'];
  if (xdg) return path.join(xdg, tool);

  const p = platform(plt);
  const home = os.homedir();

  switch (p) {
    case 'darwin':
      return path.join(home, 'Library', 'Caches', tool);
    case 'win32': {
      const local = process.env['LocalAppData'];
      if (!local) throw new Error('%LocalAppData% is not set');
      return path.win32.join(local, tool, 'cache');
    }
    default:
      return path.join(home, '.cache', tool);
  }
}

// ─── stateDir ────────────────────────────────────────────────────────────────

/**
 * Returns the state directory for the named tool.
 *
 * State stores runtime artefacts that should persist across restarts but are
 * not user-facing configuration (e.g. lock files, history, socket paths).
 *
 * Resolution order:
 * 1. `$XDG_STATE_HOME/<tool>` — when the variable is set and non-empty.
 * 2. OS-native fallback:
 *    - macOS: `~/Library/Application Support/<tool>/state`
 *      (the `/state` suffix avoids collision with {@link dataDir})
 *    - Windows: `%LocalAppData%\<tool>\state`
 *      (throws if `LocalAppData` is unset)
 *    - Linux/other: `~/.local/state/<tool>`
 *
 * @param tool - Tool name appended as the final path segment.
 * @param plt - Optional platform override (defaults to `process.platform`).
 * @returns Absolute path to the state directory.
 * @throws {Error} When the OS-native root cannot be determined.
 *
 * @example
 * ```ts
 * const dir = stateDir('myapp');
 * // macOS   → /Users/alice/Library/Application Support/myapp/state
 * // Linux   → /home/alice/.local/state/myapp
 * // Windows → C:\Users\alice\AppData\Local\myapp\state
 * ```
 */
export function stateDir(tool: string, plt?: NodeJS.Platform): string {
  const xdg = process.env['XDG_STATE_HOME'];
  if (xdg) return path.join(xdg, tool);

  const p = platform(plt);
  const home = os.homedir();

  switch (p) {
    case 'darwin':
      return path.join(home, 'Library', 'Application Support', tool, 'state');
    case 'win32': {
      const local = process.env['LocalAppData'];
      if (!local) throw new Error('%LocalAppData% is not set');
      return path.win32.join(local, tool, 'state');
    }
    default:
      return path.join(home, '.local', 'state', tool);
  }
}

// ─── mustEnsure ──────────────────────────────────────────────────────────────

/**
 * Creates `dir` (and any parents) with mode `0o750`, then returns `dir`.
 *
 * Throws an `Error` if `fs.mkdirSync` fails — for example when `dir` is an
 * existing regular file or a permission error prevents directory creation.
 *
 * Intended for startup-time path resolution where a missing directory is
 * unrecoverable:
 *
 * @param dir - Absolute path to the directory to create.
 * @returns The same `dir` value, now guaranteed to exist on disk.
 * @throws {Error} On any filesystem error (file-exists-as-non-dir, EPERM, …).
 *
 * @example
 * ```ts
 * import { dataDir, mustEnsure } from '@hop-top/kit/xdg';
 *
 * const data = mustEnsure(dataDir('mytool'));
 * // data directory now exists on disk
 * ```
 */
export function mustEnsure(dir: string): string {
  fs.mkdirSync(dir, { recursive: true, mode: 0o750 });
  // mkdirSync with recursive:true only throws on real errors; verify it's a dir.
  const stat = fs.statSync(dir);
  if (!stat.isDirectory()) {
    throw new Error(`mustEnsure: path exists but is not a directory: ${dir}`);
  }
  return dir;
}
