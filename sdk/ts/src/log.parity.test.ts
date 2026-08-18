/**
 * Load-bearing coverage for the verbosity wiring in `log.ts`.
 *
 * These feed a *constructed* ParityData (never the shared parity.json) whose
 * values differ from the shipped contract, and assert the level mapping
 * follows it. A port that re-hardcodes info/debug/trace/warn fails here.
 *
 * Mirrors `go/console/log/parity_wiring_test.go`.
 */

import { describe, expect, it } from 'vitest';

import { quietLevel, verbosityLevel } from './log';
import { parity, type ParityData } from './tui/parity';

/** A ParityData carrying only the given verbosity block. */
function contractData(
  levels: Record<string, string>,
  quiet: string,
): ParityData {
  return { verbosity: { flag: '-V', levels, quiet_override: quiet } } as unknown as ParityData;
}

// ─── Load-bearing: behavior must follow the contract, not literals ───────────

/**
 * Swaps the level names away from the shipped info/debug/trace. If
 * `withVerbose`'s mapping is hardcoded, the resolved levels stay at the
 * shipped values and this fails.
 */
describe('verbosityLevel follows contract not literals', () => {
  const d = contractData({ '0': 'error', '1': 'warn', '2': 'info' }, 'fatal');

  it('resolves each count through the contract levels table', () => {
    expect(verbosityLevel(d, 0), 'count 0 must resolve via levels["0"], not a hardcoded info')
      .toBe('error');
    expect(verbosityLevel(d, 1), 'count 1 must resolve via levels["1"], not a hardcoded debug')
      .toBe('warn');
    expect(verbosityLevel(d, 2), 'count 2 must resolve via levels["2"], not a hardcoded trace')
      .toBe('info');
  });

  it('clamps counts above the highest declared key', () => {
    expect(verbosityLevel(d, 7)).toBe('info');
  });

  it('resolves quiet through the contract override', () => {
    expect(quietLevel(d), 'quiet must resolve via quiet_override, not a hardcoded warn')
      .toBe('fatal');
  });
});

/**
 * Proves the mapping is table-driven rather than a three-branch if/else: a
 * contract declaring a 4th level must be honored.
 */
describe('verbosityLevel honors a contract beyond the shipped 0/1/2', () => {
  const d = contractData({ '0': 'warn', '3': 'trace' }, 'error');

  it('falls back to the nearest lower declared key', () => {
    expect(verbosityLevel(d, 0)).toBe('warn');
    expect(verbosityLevel(d, 2)).toBe('warn');
  });

  it('honors a key the shipped contract does not declare', () => {
    expect(verbosityLevel(d, 3)).toBe('trace');
    expect(quietLevel(d)).toBe('error');
  });
});

// ─── Requirement pin: observable behavior unchanged ──────────────────────────

/**
 * Pins the wiring to the values actually declared in parity.json, so the
 * refactor cannot silently change observable behavior. NOT load-bearing:
 * it keeps passing if a literal is restored, by design.
 */
describe('verbosityLevel matches shipped contract', () => {
  it('resolves the historical -V level ladder', () => {
    expect(parity.verbosity.levels).toEqual({ '0': 'info', '1': 'debug', '2': 'trace' });
    expect(parity.verbosity.quiet_override).toBe('warn');

    expect(verbosityLevel(parity, 0)).toBe('info');
    expect(verbosityLevel(parity, 1)).toBe('debug');
    expect(verbosityLevel(parity, 2)).toBe('trace');
    expect(verbosityLevel(parity, 3)).toBe('trace');
    expect(quietLevel(parity)).toBe('warn');
  });
});

// ─── Degradation ─────────────────────────────────────────────────────────────

describe('verbosityLevel degrades safely', () => {
  it('falls back on an empty contract', () => {
    const empty = contractData({}, '');
    expect(verbosityLevel(empty, 0)).toBe('info');
    expect(quietLevel(empty), 'absent quiet_override falls back to warn').toBe('warn');
  });

  it('falls back on unrecognized level names', () => {
    const bogus = contractData({ '0': 'nonsense', x: 'debug' }, 'nonsense');
    expect(verbosityLevel(bogus, 0), 'unrecognized level name falls back to info').toBe('info');
    expect(quietLevel(bogus)).toBe('warn');
  });
});
