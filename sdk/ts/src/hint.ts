/**
 * @module hint
 * @package @hop-top/kit
 *
 * Contextual next-step hints — mirrors Go's output/hint.go contract.
 *
 * Hints guide users (and agents) toward the logical next action without
 * burying them in a wall of text. They are suppressed when output is
 * machine-formatted (JSON/YAML), piped (non-TTY), or explicitly disabled
 * via flag/env.
 */

import type { Writable } from 'node:stream';
import type { Format } from './output.js';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/** A single next-step suggestion attached to a command. */
export interface Hint {
  /** Human-readable hint text (e.g. `"Run \`hop version\` to verify."`). */
  message: string;
  /**
   * When provided, the hint is only rendered if this returns true.
   * Omit (or set to undefined) for hints that always apply.
   */
  condition?: () => boolean;
}

/** Options read by {@link hintsEnabled} — subset of parsed CLI opts. */
export interface HintOptions {
  /** Value of --no-hints flag. */
  noHints?: boolean;
  /** Value of --quiet flag. */
  quiet?: boolean;
  /** Value of hints.enabled config key (false = disabled). */
  hintsEnabled?: boolean;
}

// ---------------------------------------------------------------------------
// HintSet
// ---------------------------------------------------------------------------

/**
 * Concurrency-safe registry mapping command names to hints.
 * Mirrors Go's `output.HintSet`.
 */
export class HintSet {
  private readonly m: Map<string, Hint[]> = new Map();

  /** Add one or more hints for the given command name. */
  register(cmd: string, ...hints: Hint[]): void {
    const existing = this.m.get(cmd) ?? [];
    this.m.set(cmd, [...existing, ...hints]);
  }

  /**
   * Return a copy of the hints registered for cmd.
   * Returns an empty array when none are registered.
   */
  lookup(cmd: string): Hint[] {
    return [...(this.m.get(cmd) ?? [])];
  }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Return only the hints whose condition is undefined or returns true. */
export function active(hints: Hint[]): Hint[] {
  return hints.filter(h => h.condition === undefined || h.condition());
}

/**
 * Report whether hints should be rendered given the current options.
 * Mirrors Go's `output.HintsEnabled`.
 *
 * Hints are disabled when any of:
 *   - `opts.noHints` is true
 *   - `opts.hintsEnabled` is explicitly false
 *   - `opts.quiet` is true
 *   - `HOP_QUIET_HINTS` env var is set to `"1"`, `"true"`, or `"yes"`
 */
export function hintsEnabled(opts: HintOptions): boolean {
  if (opts.noHints) return false;
  if (opts.hintsEnabled === false) return false;
  if (opts.quiet) return false;
  const env = process.env['HOP_QUIET_HINTS'];
  if (env === '1' || env === 'true' || env === 'yes') return false;
  return true;
}

/**
 * Write active hints to `w` with dimmed styling.
 * No-op when format is not `"table"`, stdout is not a TTY, or hints disabled.
 * Mirrors Go's `output.RenderHints`.
 *
 * @param w       Writable stream (typically `process.stdout`).
 * @param hints   Hints to consider (pass `hintSet.lookup(cmdName)`).
 * @param format  Current output format.
 * @param opts    Parsed CLI options (noHints, quiet, hintsEnabled).
 * @param muted   Hex color for the hint prefix/text (e.g. `"#858183"`).
 *                Ignored when NO_COLOR env var is set or stdout is not a TTY.
 */
export function renderHints(
  w: Writable,
  hints: Hint[],
  format: Format,
  opts: HintOptions,
  muted: string,
): void {
  if (format !== 'table') return;
  if (!hintsEnabled(opts)) return;

  // TTY check — only render to an actual terminal.
  const isTTY = (w as NodeJS.WriteStream).isTTY === true;
  if (!isTTY) return;

  const visible = active(hints);
  if (visible.length === 0) return;

  const noColor =
    process.env['NO_COLOR'] !== undefined ||
    process.argv.includes('--no-color');

  w.write('\n');
  for (const h of visible) {
    const text = `→ ${h.message}`;
    if (noColor) {
      w.write(text + '\n');
    } else {
      // ANSI 24-bit foreground SGR from hex muted color.
      const hex = muted.replace('#', '');
      const r = parseInt(hex.slice(0, 2), 16);
      const g = parseInt(hex.slice(2, 4), 16);
      const b = parseInt(hex.slice(4, 6), 16);
      w.write(`\x1b[38;2;${r};${g};${b}m${text}\x1b[0m\n`);
    }
  }
}

// ---------------------------------------------------------------------------
// Standard hint factories
// ---------------------------------------------------------------------------

/**
 * Add a standard upgrade hint: `"Run \`<binary> version\` to verify."`
 * Active only when `upgraded` returns true.
 * Mirrors Go's `output.RegisterUpgradeHints`.
 */
export function registerUpgradeHints(
  hints: HintSet,
  binary: string,
  upgraded: () => boolean,
): void {
  hints.register('upgrade', {
    message:   `Run \`${binary} version\` to verify.`,
    condition: upgraded,
  });
}

/**
 * Add a standard version hint: `"Run \`<binary> upgrade\` to get latest."`
 * Active only when `updateAvail` returns true.
 * Mirrors Go's `output.RegisterVersionHints`.
 */
export function registerVersionHints(
  hints: HintSet,
  binary: string,
  updateAvail: () => boolean,
): void {
  hints.register('version', {
    message:   `Run \`${binary} upgrade\` to get latest.`,
    condition: updateAvail,
  });
}
