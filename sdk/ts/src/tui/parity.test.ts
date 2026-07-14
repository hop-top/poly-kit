/**
 * Contract-sync tests for tui/parity.
 *
 * - Compare the vendored sdk/ts/src/tui/parity.json against the canonical
 *   contracts/parity/parity.json. If they drift, the test fails — keeping
 *   the published package self-contained without losing the single source
 *   of truth.
 * - Sanity-check the shape the loader exposes.
 */

import * as fs from 'fs';
import * as path from 'path';
import { describe, expect, it } from 'vitest';

import { parity } from './parity.js';

// ─── Contract sync ───────────────────────────────────────────────────────────

describe('tui parity contract sync', () => {
  it('local parity.json matches contracts/parity/parity.json', () => {
    const local = JSON.parse(
      fs.readFileSync(path.join(__dirname, 'parity.json'), 'utf8'),
    );
    const canonical = JSON.parse(
      fs.readFileSync(
        path.join(__dirname, '..', '..', '..', '..', 'contracts', 'parity', 'parity.json'),
        'utf8',
      ),
    );
    expect(local).toEqual(canonical);
  });
});

// ─── Loader shape ────────────────────────────────────────────────────────────

describe('parity loader', () => {
  it('exposes status symbols', () => {
    expect(parity.status.symbols.success).toBeTruthy();
    expect(parity.status.symbols.error).toBeTruthy();
  });

  it('exposes spinner frames and interval', () => {
    expect(parity.spinner.frames.length).toBeGreaterThan(0);
    expect(parity.spinner.interval_ms).toBeGreaterThan(0);
  });

  it('exposes help section order and titles', () => {
    expect(parity.help.section_order.length).toBeGreaterThan(0);
    for (const section of parity.help.section_order) {
      expect(parity.help.sections[section]).toBeDefined();
    }
  });
});
