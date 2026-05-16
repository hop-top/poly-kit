/**
 * @module tui/badge
 * @package @hop-top/kit
 *
 * Renders a styled badge/pill string.
 * Mirrors Go's badge concept from hop.top/kit/tui — pure string, no bubbletea dep.
 */

import type { Theme } from '../cli.js';

export interface BadgeOpts {
  /** Override background color (hex, e.g. "#FF0000"). Defaults to theme.accent. */
  color?: string;
}

/**
 * Renders a styled inline badge.
 *
 * Background defaults to `theme.accent`; text is rendered in white for contrast.
 * Padding of one space is added on each side (mirrors lipgloss Padding(0, 1)).
 *
 * @param theme - Active CLI theme.
 * @param text  - Badge label text.
 * @param opts  - Optional overrides.
 * @returns     ANSI-styled badge string.
 *
 * @example
 * ```ts
 * import { buildTheme } from '@hop-top/kit/cli';
 * import { badge } from '@hop-top/kit/tui';
 * console.log(badge(buildTheme(), '^ UPDATE'));
 * ```
 */
export function badge(theme: Theme, text: string, opts?: BadgeOpts): string {
  const bg = opts?.color ?? theme.accent;
  const [r, g, b] = hexToRgb(bg);
  // White text on colored background for contrast.
  const bgOpen  = `\x1b[48;2;${r};${g};${b}m`;
  const fgOpen  = `\x1b[38;2;255;255;255m`;
  const reset   = `\x1b[0m`;
  return `${bgOpen}${fgOpen} ${text} ${reset}`;
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
