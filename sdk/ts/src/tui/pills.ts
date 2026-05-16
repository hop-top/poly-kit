/**
 * @module tui/pills
 * @package @hop-top/kit
 *
 * Renders a horizontal list of styled pills.
 * Mirrors Go's Pill/PillBar concept from hop.top/kit/tui — pure string output.
 */

import type { Theme } from '../cli.js';

/**
 * Renders a space-separated row of styled pills.
 *
 * Each item is wrapped in `theme.secondary` foreground color with one space of
 * padding on each side, mirroring lipgloss Padding(0, 1) on a blurred pill.
 *
 * @param theme - Active CLI theme.
 * @param items - Labels to render as pills.
 * @returns     Space-joined ANSI-styled pill string. Empty string when no items.
 *
 * @example
 * ```ts
 * import { buildTheme } from '@hop-top/kit/cli';
 * import { pills } from '@hop-top/kit/tui';
 * console.log(pills(buildTheme(), ['branch: main', 'env: prod']));
 * ```
 */
export function pills(theme: Theme, items: string[]): string {
  if (items.length === 0) return '';
  return items.map(item => renderPill(theme.secondary, item)).join(' ');
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function renderPill(hex: string, text: string): string {
  const h = hex.replace('#', '');
  const r = parseInt(h.slice(0, 2), 16);
  const g = parseInt(h.slice(2, 4), 16);
  const b = parseInt(h.slice(4, 6), 16);
  return `\x1b[38;2;${r};${g};${b}m ${text} \x1b[0m`;
}
