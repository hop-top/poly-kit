/**
 * @module tui/parity
 * @package @hop-top/kit
 *
 * Loads tui/parity.json — the cross-language parity constants SoT.
 * All TUI modules should import constants from here, not hardcode them.
 */

import { existsSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';

interface ParityData {
  status: {
    symbols: Record<'info' | 'success' | 'error' | 'warn', string>;
  };
  spinner: {
    frames: string[];
    interval_ms: number;
  };
  anim: {
    runes: string;
    interval_ms: number;
    default_width: number;
  };
  help: {
    /** Fang-vocabulary section names in render order. */
    section_order: string[];
    /** Display metadata keyed by fang section name. */
    sections: Record<string, { title: string }>;
  };
}

// Resolve from source, built dist, or current workspace checkout.
const _candidates = [
  resolve(__dirname, '..', '..', '..', '..', 'contracts', 'parity', 'parity.json'),
  resolve(__dirname, '..', '..', '..', '..', '..', 'contracts', 'parity', 'parity.json'),
  resolve(process.cwd(), 'contracts', 'parity', 'parity.json'),
];
const _path = _candidates.find((path) => existsSync(path)) ?? _candidates[0];

export const parity: ParityData = JSON.parse(readFileSync(_path, 'utf8'));
