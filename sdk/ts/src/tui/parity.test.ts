/**
 * Drift guards for tui/parity.
 *
 * The loader reads the canonical `contracts/parity/parity.json` directly, so
 * there is no vendored second copy to keep in sync. What remains to protect
 * is the LOADER: a block can be added to parity.json and read as enforced
 * while nothing in TypeScript ever models it.
 *
 * Mirrors `TestParityNoUnloadedBlocks` and `TestParityLoadedBlocksNonZero`
 * in `contracts/parity/parity_test.go`.
 */

import * as fs from 'fs';
import * as path from 'path';
import { describe, expect, it } from 'vitest';

import { PARITY_BLOCKS, parity } from './parity.js';

const CANONICAL_PATH = path.join(
  __dirname,
  '..',
  '..',
  '..',
  '..',
  'contracts',
  'parity',
  'parity.json',
);

/** Top-level keys parity.json declares, minus `$`-prefixed schema metadata. */
function declaredBlocks(): string[] {
  const raw = JSON.parse(fs.readFileSync(CANONICAL_PATH, 'utf8')) as Record<string, unknown>;
  return Object.keys(raw).filter((k) => !k.startsWith('$'));
}

// ─── Drift guard: block registry ─────────────────────────────────────────────

describe('parity block registry', () => {
  it('models every block parity.json declares', () => {
    const known = new Set<string>(PARITY_BLOCKS);
    const unknown = declaredBlocks().filter((k) => !known.has(k));

    expect(
      unknown,
      `contracts/parity/parity.json declares block(s) ${JSON.stringify(unknown)} that the ` +
        `TypeScript loader does not know.\n` +
        `parity.json is a loaded contract, not documentation: an unloaded block is ` +
        `invisible to every test and every consumer.\n` +
        `Fix by adding the block to the ParityData interface and to PARITY_BLOCKS in ` +
        `sdk/ts/src/tui/parity.ts — or, if it is not a cross-language constant, move it ` +
        `to prose in contracts/parity/README.md.`,
    ).toEqual([]);
  });

  it('does not list blocks parity.json no longer declares', () => {
    const declared = new Set(declaredBlocks());
    const stale = PARITY_BLOCKS.filter((b) => !declared.has(b));

    expect(
      stale,
      `PARITY_BLOCKS lists ${JSON.stringify(stale)} but contracts/parity/parity.json no ` +
        `longer declares it; drop it from PARITY_BLOCKS in sdk/ts/src/tui/parity.ts.`,
    ).toEqual([]);
  });
});

// ─── Drift guard: loaded values are non-empty ────────────────────────────────

/**
 * A block wired into ParityData under a mismatched key name still parses
 * clean and leaves `undefined` behind — silent, and exactly what this
 * contract exists to prevent. Asserting the key was SEEN is not enough;
 * assert the value actually arrived.
 */
describe('parity loaded blocks are non-empty', () => {
  it('status symbols loaded', () => {
    expect(Object.keys(parity.status?.symbols ?? {}).length).toBeGreaterThan(0);
    for (const kind of ['info', 'success', 'error', 'warn'] as const) {
      expect(parity.status.symbols[kind], `status.symbols.${kind}: empty after load`).toBeTruthy();
    }
  });

  it('spinner loaded', () => {
    expect(parity.spinner?.frames?.length ?? 0).toBeGreaterThan(0);
    expect(parity.spinner?.interval_ms ?? 0).toBeGreaterThan(0);
  });

  it('anim loaded', () => {
    expect(parity.anim?.runes ?? '').toBeTruthy();
    expect(parity.anim?.interval_ms ?? 0).toBeGreaterThan(0);
    expect(parity.anim?.default_width ?? 0).toBeGreaterThan(0);
  });

  it('help loaded', () => {
    expect(parity.help?.section_order?.length ?? 0).toBeGreaterThan(0);
    expect(Object.keys(parity.help?.sections ?? {}).length).toBeGreaterThan(0);
    for (const section of parity.help.section_order) {
      expect(parity.help.sections[section], `help.sections[${section}]: missing`).toBeDefined();
    }
  });

  it('verbosity loaded', () => {
    const v = parity.verbosity;
    expect(v?.flag, `verbosity: incompletely loaded: ${JSON.stringify(v)}`).toBeTruthy();
    expect(
      Object.keys(v?.levels ?? {}).length,
      `verbosity: incompletely loaded: ${JSON.stringify(v)}`,
    ).toBeGreaterThan(0);
    expect(
      v?.quiet_override,
      `verbosity: incompletely loaded: ${JSON.stringify(v)}`,
    ).toBeTruthy();
  });

  it('streams loaded', () => {
    const s = parity.streams;
    expect(s?.flag, `streams: incompletely loaded: ${JSON.stringify(s)}`).toBeTruthy();
    expect(s?.label_format, `streams: incompletely loaded: ${JSON.stringify(s)}`).toBeTruthy();
    expect(s?.output, `streams: incompletely loaded: ${JSON.stringify(s)}`).toBeTruthy();
  });

  it('description and extends loaded', () => {
    expect(parity.description).toBeTruthy();
    expect(parity.extends?.length ?? 0).toBeGreaterThan(0);
  });
});

// ─── Value pins ──────────────────────────────────────────────────────────────

describe('parity pinned values', () => {
  it('pins verbosity contract', () => {
    expect(parity.verbosity.flag).toBe('-V');
    expect(parity.verbosity.levels).toEqual({ '0': 'info', '1': 'debug', '2': 'trace' });
    expect(parity.verbosity.quiet_override).toBe('warn');
  });

  it('pins streams contract', () => {
    expect(parity.streams.flag).toBe('--stream');
    expect(parity.streams.label_format).toBe('[{name}]');
    expect(parity.streams.output).toBe('stderr');
  });
});
