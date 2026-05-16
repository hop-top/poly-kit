/**
 * @module tui/anim
 * @package @hop-top/kit
 *
 * Gradient animation component — cycling character scramble.
 * Mirrors Go's Anim from hop.top/kit/tui — pure ANSI, no bubbletea dep.
 *
 * Cycles random chars through a gradient from theme.accent → theme.secondary
 * at ~100ms per frame. Uses \r to overwrite the current line.
 */

import type { Theme } from '../cli.js';
import { parity } from './parity.js';

const ANIM_RUNES    = parity.anim.runes;
const DEFAULT_WIDTH = parity.anim.default_width;
const INTERVAL_MS   = parity.anim.interval_ms;

export interface AnimOpts {
  /** Number of animated characters (default: 10). */
  width?: number;
  /** Optional label rendered after the animation. */
  label?: string;
}

export interface Anim {
  /** Start the animation loop. */
  start(): void;
  /** Stop the animation loop and clear the line. */
  stop(): void;
  /** Update the label shown after the animated chars. */
  setLabel(label: string): void;
}

/**
 * Creates a gradient-cycling character scramble animation.
 *
 * Colors cycle from theme.accent to theme.secondary across the width.
 * Renders to stderr; uses \r to overwrite the current line each tick.
 *
 * @param theme - Active CLI theme.
 * @param opts  - Optional width and initial label.
 * @returns     Anim control object.
 *
 * @example
 * ```ts
 * import { buildTheme } from '@hop-top/kit/cli';
 * import { anim } from '@hop-top/kit/tui';
 * const a = anim(buildTheme(), { label: 'processing' });
 * a.start();
 * // await work
 * a.stop();
 * ```
 */
export function anim(theme: Theme, opts?: AnimOpts): Anim {
  const width = opts?.width ?? DEFAULT_WIDTH;
  let label = opts?.label ?? '';

  // Pre-compute gradient: accent → secondary across `width` steps.
  const gradient = makeGradient(theme.accent, theme.secondary, width);
  const reset = `\x1b[0m`;

  let timer: ReturnType<typeof setInterval> | null = null;

  function randomChar(): string {
    return ANIM_RUNES[Math.floor(Math.random() * ANIM_RUNES.length)];
  }

  function render(): void {
    let line = '\r';
    for (let i = 0; i < width; i++) {
      const [r, g, b] = gradient[i];
      line += `\x1b[38;2;${r};${g};${b}m${randomChar()}${reset}`;
    }
    if (label) {
      line += ' ' + label;
    }
    process.stderr.write(line);
  }

  return {
    start(): void {
      if (timer !== null) return;
      render();
      timer = setInterval(render, INTERVAL_MS);
    },

    stop(): void {
      if (timer !== null) {
        clearInterval(timer);
        timer = null;
      }
      process.stderr.write('\r\x1b[K');
    },

    setLabel(newLabel: string): void {
      label = newLabel;
    },
  };
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Linear RGB blend from hexA → hexB across `steps` positions. */
export function makeGradient(
  hexA: string,
  hexB: string,
  steps: number,
): Array<[number, number, number]> {
  if (steps <= 0) return [];
  const [ar, ag, ab] = hexToRgb(hexA);
  const [br, bg, bb] = hexToRgb(hexB);
  const result: Array<[number, number, number]> = [];
  for (let i = 0; i < steps; i++) {
    const t = steps === 1 ? 0 : i / (steps - 1);
    result.push([
      Math.round(ar + (br - ar) * t),
      Math.round(ag + (bg - ag) * t),
      Math.round(ab + (bb - ab) * t),
    ]);
  }
  return result;
}

function hexToRgb(hex: string): [number, number, number] {
  const h = hex.replace('#', '');
  return [
    parseInt(h.slice(0, 2), 16),
    parseInt(h.slice(2, 4), 16),
    parseInt(h.slice(4, 6), 16),
  ];
}
