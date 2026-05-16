/**
 * @module tui/status
 * @package @hop-top/kit
 *
 * Renders a prefixed status line with kind-derived symbol + color.
 * Mirrors Go's InfoType/Status concept from hop.top/kit/tui.
 */

import type { Theme } from '../cli.js';
import { parity } from './parity.js';

const _symbols = parity.status.symbols;

/** Classification of a status message — maps to indicator symbol and color. */
export type StatusKind = 'info' | 'success' | 'error' | 'warn';

/**
 * Renders a prefixed status line.
 *
 * Indicator symbols and colors mirror Go's InfoType constants:
 *   - info    → ℹ  accent
 *   - success → ✓  success
 *   - error   → ●  error
 *   - warn    → ▲  secondary
 *
 * @param theme - Active CLI theme.
 * @param text  - Status message text.
 * @param kind  - Message classification (default: `'info'`).
 * @returns     ANSI-styled status string with indicator prefix.
 *
 * @example
 * ```ts
 * import { buildTheme } from '@hop-top/kit/cli';
 * import { status } from '@hop-top/kit/tui';
 * console.log(status(buildTheme(), 'Operation complete', 'success'));
 * ```
 */
export function status(theme: Theme, text: string, kind: StatusKind = 'info'): string {
  const { symbol, color } = resolveKind(theme, kind);
  const indicator = colorize(color, symbol);
  const message   = colorize(color, text);
  return `${indicator} ${message}`;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

interface KindSpec { symbol: string; color: string }

function resolveKind(theme: Theme, kind: StatusKind): KindSpec {
  switch (kind) {
    case 'success': return { symbol: _symbols.success, color: theme.success };
    case 'error':   return { symbol: _symbols.error,   color: theme.error };
    case 'warn':    return { symbol: _symbols.warn,    color: theme.secondary };
    default:        return { symbol: _symbols.info,    color: theme.accent };
  }
}

function colorize(hex: string, text: string): string {
  const h = hex.replace('#', '');
  const r = parseInt(h.slice(0, 2), 16);
  const g = parseInt(h.slice(2, 4), 16);
  const b = parseInt(h.slice(4, 6), 16);
  return `\x1b[38;2;${r};${g};${b}m${text}\x1b[0m`;
}
