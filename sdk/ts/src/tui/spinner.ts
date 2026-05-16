/**
 * @module tui/spinner
 * @package @hop-top/kit
 *
 * Terminal spinner component.
 * Mirrors Go's NewSpinner from hop.top/kit/tui — pure ANSI, no bubbletea dep.
 */

import type { Theme } from '../cli.js';
import { parity } from './parity.js';

const FRAMES      = parity.spinner.frames;
const INTERVAL_MS = parity.spinner.interval_ms;

export interface Spinner {
  /** Start spinning with optional message. */
  start(msg?: string): void;
  /** Stop spinner, print optional final message. */
  stop(msg?: string): void;
  /** Update the spinner message while running. */
  message(msg: string): void;
}

/**
 * Creates a terminal spinner styled with theme.accent.
 *
 * Renders to stderr; uses \r to overwrite the current line each frame.
 *
 * @param theme - Active CLI theme.
 * @returns     Spinner control object.
 *
 * @example
 * ```ts
 * import { buildTheme } from '@hop-top/kit/cli';
 * import { spinner } from '@hop-top/kit/tui';
 * const s = spinner(buildTheme());
 * s.start('Loading…');
 * // await work
 * s.stop('Done');
 * ```
 */
export function spinner(theme: Theme): Spinner {
  const [r, g, b] = hexToRgb(theme.accent);
  const accentOpen = `\x1b[38;2;${r};${g};${b}m`;
  const reset = `\x1b[0m`;

  let timer: ReturnType<typeof setInterval> | null = null;
  let frameIdx = 0;
  let currentMsg = '';

  function render(): void {
    const frame = FRAMES[frameIdx % FRAMES.length];
    const line = `\r${accentOpen}${frame}${reset} ${currentMsg}`;
    process.stderr.write(line);
    frameIdx++;
  }

  return {
    start(msg = ''): void {
      if (timer !== null) return;
      currentMsg = msg;
      frameIdx = 0;
      render();
      timer = setInterval(render, INTERVAL_MS);
    },

    stop(msg?: string): void {
      if (timer !== null) {
        clearInterval(timer);
        timer = null;
      }
      // Clear spinner line.
      process.stderr.write('\r\x1b[K');
      if (msg !== undefined) {
        process.stderr.write(`${msg}\n`);
      }
    },

    message(msg: string): void {
      currentMsg = msg;
    },
  };
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function hexToRgb(hex: string): [number, number, number] {
  const h = hex.replace('#', '');
  return [
    parseInt(h.slice(0, 2), 16),
    parseInt(h.slice(2, 4), 16),
    parseInt(h.slice(4, 6), 16),
  ];
}
