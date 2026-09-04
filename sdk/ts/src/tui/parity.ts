/**
 * @module tui/parity
 * @package @hop-top/kit
 *
 * Cross-language parity constants SoT. Values are read from the canonical
 * `contracts/parity/parity.json` at build time — tsup/esbuild inlines the
 * JSON into the emitted bundle, so the published package stays
 * self-contained without vendoring a second copy of the file.
 *
 * All TUI modules should import constants from here, not hardcode them.
 */

import parityRaw from '../../../../contracts/parity/parity.json';

/** Display metadata for a single help section. */
export interface SectionConfig {
  title: string;
}

/**
 * ParityData models every content block `parity.json` declares.
 *
 * A block present in the JSON but absent here is decoration: nothing loads
 * it, so no test and no consumer can see it drift. `PARITY_BLOCKS` below is
 * the machine-checkable half of that rule.
 */
export interface ParityData {
  description: string;
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
    sections: Record<string, SectionConfig>;
  };
  verbosity: {
    flag: string;
    /**
     * Maps the stacked `-V` count (as a decimal string) to a log level name.
     * Keys are strings because JSON object keys are.
     */
    levels: Record<string, string>;
    quiet_override: string;
  };
  streams: {
    flag: string;
    label_format: string;
    output: string;
  };
  /** Sibling contract files the parity suite also covers. */
  extends: string[];
}

/**
 * PARITY_BLOCKS is the registry of top-level `parity.json` keys this loader
 * knows. Every content block in `parity.json` MUST appear here and MUST have
 * a corresponding property on `ParityData` — the drift guard in
 * `parity.test.ts` enforces both directions so a new block cannot be added
 * as decoration.
 *
 * Mirrors `parity.Blocks` in `contracts/parity/parity.go`.
 *
 * Keys starting with `$` are JSON Schema metadata, not content.
 */
export const PARITY_BLOCKS = [
  'description',
  'status',
  'spinner',
  'anim',
  'help',
  'verbosity',
  'streams',
  'extends',
] as const;

export const parity: ParityData = parityRaw;
