/**
 * @module tui/parity
 * @package @hop-top/kit
 *
 * Cross-language parity constants SoT. The canonical file lives at
 * `contracts/parity/parity.json`; a vendored copy ships next to this
 * module so the published package is self-contained. A contract-sync
 * test keeps the two in lockstep.
 *
 * All TUI modules should import constants from here, not hardcode them.
 */

import parityRaw from './parity.json';

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

export const parity: ParityData = parityRaw;
