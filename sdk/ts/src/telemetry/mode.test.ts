import { describe, it, expect } from 'vitest';
import { Mode, parseMode, resolveMode } from './mode';

// Helper: build an isolated env map per test. We never mutate
// process.env directly; resolveMode accepts an injected map so the
// tests stay hermetic.
function env(vars: Record<string, string | undefined>): NodeJS.ProcessEnv {
  const out: NodeJS.ProcessEnv = {};
  for (const k of Object.keys(vars)) {
    const v = vars[k];
    if (v !== undefined) {
      out[k] = v;
    }
  }
  return out;
}

// ─── parseMode ──────────────────────────────────────────────────────────────

describe('parseMode', () => {
  it('returns [Off, true] for empty / null / undefined', () => {
    expect(parseMode('')).toEqual([Mode.Off, true]);
    expect(parseMode(undefined)).toEqual([Mode.Off, true]);
    expect(parseMode(null)).toEqual([Mode.Off, true]);
  });

  it('parses "off" case-insensitively', () => {
    expect(parseMode('off')).toEqual([Mode.Off, true]);
    expect(parseMode('OFF')).toEqual([Mode.Off, true]);
    expect(parseMode('Off')).toEqual([Mode.Off, true]);
  });

  it('parses "anon" case-insensitively', () => {
    expect(parseMode('anon')).toEqual([Mode.Anon, true]);
    expect(parseMode('ANON')).toEqual([Mode.Anon, true]);
    expect(parseMode('Anon')).toEqual([Mode.Anon, true]);
  });

  it('parses "full" case-insensitively', () => {
    expect(parseMode('full')).toEqual([Mode.Full, true]);
    expect(parseMode('FULL')).toEqual([Mode.Full, true]);
    expect(parseMode('Full')).toEqual([Mode.Full, true]);
  });

  it('trims surrounding whitespace', () => {
    expect(parseMode('  anon  ')).toEqual([Mode.Anon, true]);
    expect(parseMode('\tfull\n')).toEqual([Mode.Full, true]);
  });

  it('returns [Off, true] for whitespace-only', () => {
    // Mirrors Go: trim → "" → empty branch → [Off, true].
    expect(parseMode('   ')).toEqual([Mode.Off, true]);
  });

  it('returns [Off, false] for unknown tokens', () => {
    expect(parseMode('garbage')).toEqual([Mode.Off, false]);
    expect(parseMode('verbose')).toEqual([Mode.Off, false]);
    expect(parseMode('on')).toEqual([Mode.Off, false]);
  });
});

// ─── resolveMode ────────────────────────────────────────────────────────────

describe('resolveMode', () => {
  it('returns Off when no env vars are set', () => {
    expect(resolveMode(env({}))).toBe(Mode.Off);
  });

  it('reads KIT_TELEMETRY_MODE when no app prefix is registered', () => {
    expect(resolveMode(env({ KIT_TELEMETRY_MODE: 'anon' }))).toBe(Mode.Anon);
    expect(resolveMode(env({ KIT_TELEMETRY_MODE: 'full' }))).toBe(Mode.Full);
    expect(resolveMode(env({ KIT_TELEMETRY_MODE: 'off' }))).toBe(Mode.Off);
  });

  it('case-insensitively reads KIT_TELEMETRY_MODE', () => {
    expect(resolveMode(env({ KIT_TELEMETRY_MODE: 'ANON' }))).toBe(Mode.Anon);
    expect(resolveMode(env({ KIT_TELEMETRY_MODE: 'Full' }))).toBe(Mode.Full);
  });

  it('returns Off when KIT_TELEMETRY_MODE is unparseable', () => {
    expect(resolveMode(env({ KIT_TELEMETRY_MODE: 'garbage' }))).toBe(Mode.Off);
  });

  it('honors <APP>_TELEMETRY_MODE over KIT_TELEMETRY_MODE when both are set', () => {
    // Canonical precedence example from the user spec.
    const got = resolveMode(
      env({
        KIT_APP_PREFIX: 'spaced',
        SPACED_TELEMETRY_MODE: 'full',
        KIT_TELEMETRY_MODE: 'anon',
      }),
    );
    expect(got).toBe(Mode.Full);
  });

  it('uppercases KIT_APP_PREFIX before composing the env var name', () => {
    // Lowercase prefix → still resolves to SPACED_TELEMETRY_MODE.
    const got = resolveMode(
      env({
        KIT_APP_PREFIX: 'spaced',
        SPACED_TELEMETRY_MODE: 'anon',
      }),
    );
    expect(got).toBe(Mode.Anon);
  });

  it('falls back to KIT_TELEMETRY_MODE when the app-specific var is unset', () => {
    const got = resolveMode(
      env({
        KIT_APP_PREFIX: 'spaced',
        KIT_TELEMETRY_MODE: 'anon',
      }),
    );
    expect(got).toBe(Mode.Anon);
  });

  it('falls back to KIT_TELEMETRY_MODE when the app-specific var is unparseable', () => {
    const got = resolveMode(
      env({
        KIT_APP_PREFIX: 'spaced',
        SPACED_TELEMETRY_MODE: 'garbage',
        KIT_TELEMETRY_MODE: 'full',
      }),
    );
    expect(got).toBe(Mode.Full);
  });

  it('returns Off when both vars are unparseable', () => {
    const got = resolveMode(
      env({
        KIT_APP_PREFIX: 'spaced',
        SPACED_TELEMETRY_MODE: 'maybe',
        KIT_TELEMETRY_MODE: 'verbose',
      }),
    );
    expect(got).toBe(Mode.Off);
  });

  it('ignores an empty / whitespace-only KIT_APP_PREFIX', () => {
    const got = resolveMode(
      env({
        KIT_APP_PREFIX: '   ',
        KIT_TELEMETRY_MODE: 'anon',
      }),
    );
    expect(got).toBe(Mode.Anon);
  });
});
