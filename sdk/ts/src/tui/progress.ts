/**
 * @module tui/progress
 * @package @hop-top/kit
 *
 * Terminal progress bar component.
 * Mirrors Go's Progress from hop.top/kit/tui — pure ANSI, no bubbletea dep.
 *
 * Renders a 30-char ASCII bar: filled chars in theme.accent, empty in theme.muted.
 */

import type { Theme } from '../cli.js';

const BAR_WIDTH = 30;
const FILL_CHAR = '█';
const EMPTY_CHAR = '░';

export interface Progress {
  /** Start the progress bar with optional message. */
  start(msg?: string): void;
  /** Update progress value (0.0–1.0) with optional message. */
  update(value: number, msg?: string): void;
  /** Stop the progress bar with optional final message. */
  stop(msg?: string): void;
}

/**
 * Creates a terminal progress bar styled with theme colors.
 *
 * Fill chars rendered in theme.accent; empty track in theme.muted.
 * Renders to stderr using \r to overwrite the current line.
 *
 * @param theme - Active CLI theme.
 * @param total - Optional total units (unused — value is always 0.0–1.0 ratio).
 * @returns     Progress control object.
 *
 * @example
 * ```ts
 * import { buildTheme } from '@hop-top/kit/cli';
 * import { progress } from '@hop-top/kit/tui';
 * const p = progress(buildTheme());
 * p.start('Downloading…');
 * p.update(0.5, 'Half done');
 * p.stop('Complete');
 * ```
 */
export function progress(theme: Theme, _total?: number): Progress {
  const [ar, ag, ab] = hexToRgb(theme.accent);
  const [mr, mg, mb] = hexToRgb(theme.muted);
  const accentOpen = `\x1b[38;2;${ar};${ag};${ab}m`;
  const mutedOpen  = `\x1b[38;2;${mr};${mg};${mb}m`;
  const reset = `\x1b[0m`;

  let currentValue = 0;
  let currentMsg = '';
  let running = false;

  function renderBar(value: number): string {
    const clamped = clamp01(value);
    const filled = Math.round(clamped * BAR_WIDTH);
    const empty = BAR_WIDTH - filled;
    const bar =
      accentOpen + FILL_CHAR.repeat(filled) + reset +
      mutedOpen  + EMPTY_CHAR.repeat(empty)  + reset;
    const pct = Math.round(clamped * 100);
    return `\r${bar} ${pct}%${currentMsg ? ' ' + currentMsg : ''}`;
  }

  return {
    start(msg = ''): void {
      running = true;
      currentMsg = msg;
      currentValue = 0;
      process.stderr.write(renderBar(0));
    },

    update(value: number, msg?: string): void {
      currentValue = clamp01(value);
      if (msg !== undefined) currentMsg = msg;
      if (running) {
        process.stderr.write(renderBar(currentValue));
      }
    },

    stop(msg?: string): void {
      running = false;
      // Clear line.
      process.stderr.write('\r\x1b[K');
      if (msg !== undefined) {
        process.stderr.write(`${msg}\n`);
      }
    },
  };
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

export function clamp01(v: number): number {
  if (v < 0) return 0;
  if (v > 1) return 1;
  return v;
}

/** Render a plain bar string (no ANSI) for testing. */
export function renderBarPlain(value: number): string {
  const clamped = clamp01(value);
  const filled = Math.round(clamped * BAR_WIDTH);
  const empty = BAR_WIDTH - filled;
  return FILL_CHAR.repeat(filled) + EMPTY_CHAR.repeat(empty);
}

function hexToRgb(hex: string): [number, number, number] {
  const h = hex.replace('#', '');
  return [
    parseInt(h.slice(0, 2), 16),
    parseInt(h.slice(2, 4), 16),
    parseInt(h.slice(4, 6), 16),
  ];
}
